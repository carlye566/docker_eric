package daemon

import (
	"time"
	"sync"
	"os"
	"fmt"
	"path/filepath"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
	"os/exec"
	"syscall"
)

const (
	container_monitor = "container_monitor"
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
	log("init %s", container_monitor)
	reexec.Register(container_monitor, reexecMonitor)
}

var monitor orphanedContainerMonitor

func reexecMonitor() {
	var (
		containerId = os.Args[1]
		confDir = os.Args[2]
	)
	container := &Container{
		CommonContainer: CommonContainer{
			root: confDir,
		},
	}
	if err := container.FromDisk(); err != nil {
		log("Error load config %v", err)
	} else {
		log("success to load %s config, %v", containerId, container.Config)
	}

	if err := container.readHostConfig(); err != nil {
		log("Error load hostconfig %v", err)
	} else {
		log("success to load %s hostconfig, %v", containerId, container.hostConfig)
	}

	if err := container.readCommand(); err != nil {
		log("Error load command %v", err)
	} else {
		log("success to load %s command, %v", containerId, container.command)
	}
	monitor = newOrphanedContainerMonitor(container, container.hostConfig.RestartPolicy)
}

func log(message string, args ...interface{}) {
	fmt.Printf(message+"\n", args...)
	logrus.Printf(message+"\n", args...)
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
	exec.Start()
	m.callback(10001)
	exec.Wait()
	log("container started")
	return nil
}

// callback ensures that the container's state is properly updated after we
// received ack from the execution drivers
func (m orphanedContainerMonitor) callback(pid int) {
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
		SysProcAttr: &syscall.SysProcAttr{
			Pdeathsig: syscall.SIGTERM, // send a sigterm to the proxy if the daemon process dies
		},
	}
}