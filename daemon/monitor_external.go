package daemon

import (
	"os"
	"os/exec"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
	"strings"
	"github.com/docker/docker/daemon/execdriver"
	"fmt"
)

type externalMonitor struct {
	commonMonitor
	startErr error
}

func newExternalMonitor(container *Container, policy runconfig.RestartPolicy) *externalMonitor {
	return &externalMonitor{
		commonMonitor: commonMonitor{
			restartPolicy:    policy,
			container:        container,
			stopChan:         make(chan struct{}),
			startSignal:      make(chan struct{}),
		},
	}
}

// Stop signals to the container monitor that it should stop monitoring the container
// for exits the next time the process dies
func (m externalMonitor) ExitOnNext() {
}

// Close closes the container's resources such as networking allocations and
// unmounts the contatiner's root filesystem
func (m externalMonitor) Close() error {
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

func (m externalMonitor) Start() error {
	exec := newMonitorCommand(m.container.ID, m.container.root)
	err := exec.Start()
	if err != nil {
		logrus.Errorf("%s", err)
		return err
	}
	if m.startErr != nil {
		return m.startErr
	}
	err = exec.Wait()  //to do wait for stop
	if err != nil {
		logrus.Errorf("%s", err)
		return err
	}
	return nil
}

// callback ensures that the container's state is properly updated after we
// received ack from the execution drivers
func (m externalMonitor) callback(processConfig *execdriver.ProcessConfig, pid int) {
}

func newMonitorCommand(containerId, containerRoot string) *exec.Cmd {
	args := []string{
		docker_monitor,
		containerId,
		containerRoot[0:strings.LastIndex(containerRoot, "/")], // /var/lib/docker/containers
	}
	return &exec.Cmd{
		Path: reexec.Self(),
		Args: args,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

func (daemon *Daemon) ContainerMonitorStart(id string, status StartStatus) error {
	logrus.Infof("container monitor start %v", status)
	container, err := daemon.Get(id)
	if err != nil {
		return err
	}

	if container.Paused {
		return fmt.Errorf("Cannot start a paused container, try unpause instead.")
	}

	if container.Running {
		return fmt.Errorf("Container already started")
	}
	startSignal := container.monitor.StartSignal()
	if status.Err != "" {
		//TODO set container status
		externalMonitor := container.monitor.(externalMonitor)
		externalMonitor.startErr = fmt.Errorf(status.Err)
	} else {
		container.setRunning(status.Pid)
	}
	close(startSignal)
	return nil
}

func (daemon *Daemon) ContainerMonitorStop(id string, status StopStatus) error {
	container, err := daemon.Get(id)
	if err != nil {
		return err
	}

	if !container.Running {
		return fmt.Errorf("Container already stoped")
	}
	container.setStopped(&status.ExitStatus)
	return nil
}