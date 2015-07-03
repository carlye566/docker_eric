package daemon

import (
	"time"
	"sync"
	"os"
	"os/exec"
	"fmt"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
	"github.com/docker/docker/daemon/execdriver/native"
	"path/filepath"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/docker/docker/pkg/broadcastwriter"
	"io/ioutil"
	"github.com/docker/docker/pkg/ioutils"
	"github.com/docker/docker/daemon/execdriver"
)

const (
	container_monitor = "container_monitor"
	root = "/var/run/docker"
	fakeSelf = "/usr/bin/docker"
)

type orphanedContainerMonitor struct {
	mux sync.Mutex

	// container is the container being monitored
	container *Container

	// startSignal is a channel that is closes after the container initially starts
	startSignal chan struct{}

	// stopChan is used to signal to the monitor whenever there is a wait for the
	// next restart so that the timeIncrement is not honored and the user is not
	// left waiting for nothing to happen during this time
	stopChan chan struct{}
	// lastStartTime is the time which the monitor last exec'd the container's process
	startTime time.Time
}

func init() {
	reexec.RegisterSelf(container_monitor, reexecMonitor, fakeSelf)
}

func newOrphanedContainerMonitor(container *Container, policy runconfig.RestartPolicy) *orphanedContainerMonitor {
	return &orphanedContainerMonitor{
		container: container,
		stopChan:      make(chan struct{}),
		startSignal:   make(chan struct{}),
	}
}

// Stop signals to the container monitor that it should stop monitoring the container
// for exits the next time the process dies
func (m orphanedContainerMonitor) ExitOnNext() {
	m.mux.Lock()
	close(m.stopChan)
	m.mux.Unlock()
}

// Close closes the container's resources such as networking allocations and
// unmounts the contatiner's root filesystem
func (m orphanedContainerMonitor) Close() error {
	// Cleanup networking and mounts
	m.container.cleanup()

	// FIXME: here is race condition between two RUN instructions in Dockerfile
	// because they share same runconfig and change image. Must be fixed
	// in builder/builder.go
	if err := m.container.toDisk(); err != nil {
		logrus.Errorf("Error dumping container %s state to disk: %s", m.container.ID, err)

		return err
	}

	return nil
}

func (m orphanedContainerMonitor) Start() error {
	exec := newMonitorCommand(m.container.ID, m.container.root)
	err := exec.Start()
	if err != nil {
		logrus.Errorf("%s", err)
		return err
	}
	m.callback(nil, 10001)
	err = exec.Wait()
	if err != nil {
		logrus.Errorf("%s", err)
		return err
	}
	logrus.Infof("container started")
	return nil
}

// callback ensures that the container's state is properly updated after we
// received ack from the execution drivers
func (m orphanedContainerMonitor) callback(processConfig *execdriver.ProcessConfig, pid int) {
	m.container.setRunning(pid)

	// signal that the process has started
	// close channel only if not closed
	select {
	case <-m.startSignal:
	default:
		close(m.startSignal)
	}

	if err := m.container.ToDisk(); err != nil {
		logrus.Debugf("%s", err)
	}
}

func (m orphanedContainerMonitor) StartSignal() chan struct{} {
	return m.startSignal
}

func newMonitorCommand(containerId, bashDir string) *exec.Cmd {
	args := []string{
		container_monitor,
		containerId,
		bashDir,
	}
	return &exec.Cmd{
		Path: reexec.Self(),
		Args: args,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

var monitor *orphanedContainerMonitor

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
	monitor = newOrphanedContainerMonitor(container, container.hostConfig.RestartPolicy)
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

func (m orphanedContainerMonitor) startContainer(d execdriver.Driver) error {
	pipes := execdriver.NewPipes(m.container.stdin, m.container.stdout, m.container.stderr, m.container.Config.OpenStdin)
	m.startTime = time.Now()
	log("orphanedContainerMonitor before run+")
	if _, err := d.Run(m.container.command, pipes, m.callback); err != nil {
		log("orphanedContainerMonitor after run err %v-", err)
		return err
	}
	log("orphanedContainerMonitor after run-")
	return nil
}