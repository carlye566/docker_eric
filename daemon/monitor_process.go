package daemon
import (
	"io/ioutil"
	"os"
	"path/filepath"
	"fmt"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/pkg/broadcastwriter"
	"github.com/docker/docker/daemon/execdriver/native"
	"github.com/docker/docker/daemon/execdriver"
)

const (
	docker_monitor = "docker_monitor"

// TODO this needs to be addressed
	root = "/var/run/docker"
// TODO this needs to be addressed
	fakeSelf = "/usr/bin/docker"
)

type dockerMonitor struct {
	commonMonitor
}

func init() {
	reexec.RegisterSelf(docker_monitor, reexecMonitor, fakeSelf)
}

func newDockerMonitor(container *Container, policy runconfig.RestartPolicy) *dockerMonitor {
	return &dockerMonitor{
		commonMonitor: commonMonitor{
			restartPolicy:    policy,
			container:        container,
			stopChan:         make(chan struct{}),
			startSignal:      make(chan struct{}),
		},
	}
}

var monitor *dockerMonitor

func reexecMonitor() {
	var (
		containerId = os.Args[1]
		confDir = os.Args[2]
	)
	container := &Container{
		CommonContainer: CommonContainer{
			ID:    containerId,
			State: NewState(),
			root: confDir,
			StreamConfig: StreamConfig{
				stdout: broadcastwriter.New(),
				stderr: broadcastwriter.New(),
				stdinPipe: ioutils.NopWriteCloser(ioutil.Discard),
			},
		},
	}
	if err := container.FromDisk(); err != nil {
		log("Error load config %v", err)
	}

	if err := container.readHostConfig(); err != nil {
		log("Error load hostconfig %v", err)
	}

	if err := container.readCommand(); err != nil {
		log("Error load command %v", err)
	}
	//TODO env in ProcessConfig.execCmd should be changed to ProcessConfig.env
	env := container.createDaemonEnvironment([]string{})
	container.command.ProcessConfig.Env = env
	monitor = newDockerMonitor(container, container.hostConfig.RestartPolicy)
	sysInitPath := filepath.Join(root, "init", fmt.Sprintf("dockerinit-%s", dockerversion.VERSION))
	execRoot := filepath.Join(root, "execdriver", "native")
	driver, err := native.NewDriver(execRoot, sysInitPath, []string{})
	if err != nil {
		log("new native driver err %v", err)
	}
	err = monitor.startContainer(driver)
	if err != nil {
		log("start container err %v", err)
	}
	time.Sleep(30 *time.Second)
}

func log(message string, args ...interface{}) {
	logrus.Printf("[monitor] "+message, args...)
}

func (m dockerMonitor) startContainer(d execdriver.Driver) error {
	pipes := execdriver.NewPipes(m.container.stdin, m.container.stdout, m.container.stderr, m.container.Config.OpenStdin)
	m.startTime = time.Now()
	if _, err := d.Run(m.container.command, pipes, m.callback); err != nil {
		log("launch container failed %v", err)
		return err
	}
	return nil
}
