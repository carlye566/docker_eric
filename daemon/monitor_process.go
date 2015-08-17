package daemon
import (
	"io/ioutil"
	"os"
	"path/filepath"
	"fmt"
	"time"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/opts"
	"github.com/docker/docker/runconfig"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/broadcastwriter"
	"github.com/docker/docker/daemon/execdriver/native"
	"github.com/docker/docker/daemon/execdriver"
	"strconv"
	"bytes"
	"net"
	"net/http"
	"net/http/httputil"
	"encoding/json"
	"io"
)

const (
	dockerMonitor = "docker_monitor"
	monitorPidFile = "monitor.pid"
	startStatusFile = "start_status"
	stopStatusFile = "stop_status"

// TODO this needs to be addressed
	root = "/var/run/docker"

	httpRetryTimes = 5
	httpRetryIntervalSecond = 3
)

var (
	daemonHost = fmt.Sprintf("unix://%s", opts.DefaultUnixSocket)
	notifyStartPath = "/monitor/%s/start"
	notifyStopPath = "/monitor/%s/stop"
)

type DockerMonitor struct {
	commonMonitor
	WaitAttach chan struct{}
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
		WaitAttach:           make(chan struct{}),
	}
}

func InitDockerMonitor() *DockerMonitor {
	var (
		containerId = os.Args[1]
		root = os.Args[2]
	)
	daemonHost = os.Args[3]
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
	if err := dumpToDisk(container.root, monitorPidFile, []byte(strconv.Itoa(os.Getpid()))); err != nil {
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
	if container.Config.Attach() {
		logrus.Infof("wait for attach before")
		<-monitor.WaitAttach
		logrus.Infof("wait for attach end")
	}
	//TODO address this to docker community
	if container.Config.OpenStdin {
		container.stdin, container.stdinPipe = io.Pipe()
	} else {
		container.stdinPipe = ioutils.NopWriteCloser(ioutil.Discard) // Silently drop stdin
	}
	if err := container.startLogging(); err != nil {
		fail("start logging failed %v", err)
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
	logrus.Debugf("notify_start_url = %s%s", daemonHost, notifyStartPath)
	return m.notifyDaemon(daemonHost, notifyStartPath, startStatusFile, d)
}

func (m DockerMonitor) notifyStop(status StopStatus) error {
	d, err := json.Marshal(status)
	if err != nil {
		return err
	}
	return m.notifyDaemon(daemonHost, notifyStopPath, stopStatusFile, d)
}

func (m DockerMonitor) notifyDaemon(host, path, file string, d []byte) error {
	if err := dumpToDisk(m.container.root, file, d); err != nil {
		return err
	}
	for i := 0; i < httpRetryTimes; i++ {
		if err := httpNotify(host, path, m.container.ID, d); err != nil {
			logrus.Infof("http notify daemon %s failed %v, retry %d times", string(d), err, i)
			if i == httpRetryTimes - 1 {
				return err
			}
		} else {
			break
		}
		time.Sleep(httpRetryIntervalSecond * time.Second)
	}
	return nil
}

func (m DockerMonitor) Container() *Container {
	//TODO add unit tests to confirm it works
	m.container.WaitRunning(-1)
	return m.container
}

func httpNotify(host, path, cid string, data []byte) error {
	protoAddrParts := strings.SplitN(host, "://", 2)
	contentReader := bytes.NewReader(data)
	req, err := http.NewRequest("POST", fmt.Sprintf(path, cid), contentReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.URL.Host = protoAddrParts[1]

	dial, err := net.Dial(protoAddrParts[0], protoAddrParts[1])
	if err != nil {
		return err
	}
	clientconn := httputil.NewClientConn(dial, nil)
	defer clientconn.Close()
	resp, err := clientconn.Do(req)

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
	return filepath.Join(container.root, startStatusFile)
}

func (container *Container) stopStatusPath() string {
	return filepath.Join(container.root, stopStatusFile)
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

