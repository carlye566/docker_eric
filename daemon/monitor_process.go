package daemon
import (
	"io/ioutil"
	"os"
	"path/filepath"
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/runconfig"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/broadcastwriter"
	"github.com/docker/docker/daemon/execdriver/native"
	"github.com/docker/docker/daemon/execdriver"
	"strconv"
	"bytes"
	"net/http"
	"encoding/json"
)

const (
	docker_monitor = "docker_monitor"
	monitor_pid_file = "monitor.pid"
	start_status_file = "start_status"
	stop_status_file = "stop_status"

// TODO this needs to be addressed
	root = "/var/run/docker"

	http_retry_times = 5
	http_retry_interval_second = 3

	notify_start_url = "http://127.0.0.1:2375/monitor/%s/start"
	notify_stop_url = "http://127.0.0.1:2375/monitor/%s/stop"
)

type DockerMonitor struct {
	commonMonitor
}

type StartStatus struct {
	Pid int
	Err string
	//TODO start time
}

type StopStatus struct  {
	execdriver.ExitStatus
	Err string
}

func newDockerMonitor(container *Container, policy runconfig.RestartPolicy) *DockerMonitor {
	return &DockerMonitor{
		commonMonitor: commonMonitor{
			restartPolicy:    policy,
			container:        container,
			stopChan:         make(chan struct{}),
			startSignal:      make(chan struct{}),
		},
	}
}

func InitDockerMonitor() *DockerMonitor {
	var (
		containerId = os.Args[1]
		root = os.Args[2]
	)
	container := &Container{
		CommonContainer: CommonContainer{
			ID:    containerId,
			State: NewState(),
			root: filepath.Join(root, containerId),
			StreamConfig: StreamConfig{
				stdout: broadcastwriter.New(),
				stderr: broadcastwriter.New(),
				stdinPipe: ioutils.NopWriteCloser(ioutil.Discard),
			},
		},
	}
	if err := dumpToDisk(container.root, monitor_pid_file, []byte(strconv.Itoa(os.Getpid()))); err != nil {
		fail("Error dump pid %v", err)
	}
	if err := container.FromDisk(); err != nil {
		fail("Error load config %v", err)
	}

	if err := container.readHostConfig(); err != nil {
		fail("Error load hostconfig %v", err)
	}

	if err := container.readCommand(); err != nil {
		fail("Error load command %v", err)
	}
	//TODO env in ProcessConfig.execCmd should be changed to ProcessConfig.env
	env := container.createDaemonEnvironment([]string{})
	container.command.ProcessConfig.Env = env
	return newDockerMonitor(container, container.hostConfig.RestartPolicy)
}

func (monitor DockerMonitor) Start() {
	container := monitor.container
	sysInitPath := filepath.Join(root, "init", fmt.Sprintf("dockerinit-%s", dockerversion.VERSION))
	execRoot := filepath.Join(root, "execdriver", "native")
	driver, err := native.NewDriver(execRoot, sysInitPath, []string{})
	if err != nil {
		fail("new native driver err %v", err)
	}
	pipes := execdriver.NewPipes(container.stdin, container.stdout, container.stderr, container.Config.OpenStdin)
	monitor.startTime = time.Now()
	var exitStatus execdriver.ExitStatus
	exitStatus, err = driver.Run(container.command, pipes, monitor.callback)
	var errStr string
	if err != nil {
		errStr = err.Error()
	}
	monitor.notifyStop(StopStatus{
		ExitStatus:  exitStatus,
		Err:         errStr,
	})
	if err != nil {
		fail("start container err %v", err)
	}
	logrus.Infof("[monitor] I'm going to shutdown")
}

func fail(message string, args ...interface{}) {
	logrus.Printf("[monitor] "+message, args...)
	os.Exit(1)
}

func dumpToDisk(containerRoot, file string, data []byte) error {
	f := filepath.Join(containerRoot, file)
	return ioutil.WriteFile(f, data, 0666)
}

func (m DockerMonitor) callback(processConfig *execdriver.ProcessConfig, pid int) {
	m.container.setRunning(pid)
	if err := m.container.ToDisk(); err != nil {
		logrus.Debugf("%s", err)
	}
	logrus.Infof("[monitor] pid %d", pid)
	m.notifyStart(StartStatus{Pid: pid})

}

func (m DockerMonitor) notifyStart(status StartStatus) error {
	d, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return m.notifyDaemon(notify_start_url, start_status_file, d)
}

func (m DockerMonitor) notifyStop(status StopStatus) error {
	d, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return m.notifyDaemon(notify_stop_url, stop_status_file, d)
}

func (m DockerMonitor) notifyDaemon(url, file string, d []byte) error {
	if err := dumpToDisk(m.container.root, file, d); err != nil {
		return err
	}
	for i := 0; i < http_retry_times; i++ {
		if err := httpNotify(url, m.container.ID, d); err != nil {
			logrus.Infof("http notify daemon %s failed %v, retry %d times", string(d), err, i)
			if i == http_retry_times-1 {
				return err
			}
		} else {
			break
		}
		time.Sleep(http_retry_interval_second * time.Second)
	}
	return nil
}

func (m DockerMonitor) Container() *Container {
	//TODO add unit tests to confirm it works
	m.container.WaitRunning(-1)
	return m.container
}

func httpNotify(url, cid string, data []byte) error {
	contentReader := bytes.NewReader(data)
	req, err := http.NewRequest("POST", fmt.Sprintf(url, cid), contentReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return  err
	}
	if (resp.StatusCode == http.StatusNoContent) {
		return nil
	}
	body, _ := ioutil.ReadAll(resp.Body)
	return fmt.Errorf(string(body))
}

func (container *Container) startStatusPath() string {
	return filepath.Join(container.root, start_status_file)
}

func (container *Container) stopStatusPath() string {
	return filepath.Join(container.root, stop_status_file)
}

func (container *Container) loadStopStatus() (error, StopStatus) {
	var status StopStatus
	pth := container.stopStatusPath()
	_, err := os.Stat(pth)
	if os.IsNotExist(err) {
		return fmt.Errorf("stop status file not exits"), status
	}
	f, err := os.Open(pth)
	if err != nil {
		return fmt.Errorf("Error open stop status file %s", pth), status
	}
	defer f.Close()
	json.NewDecoder(f).Decode(&status)
	return nil, status
}

