package daemon

import (
	"os"
	"os/exec"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
)

type externalMonitor struct {
	commonMonitor
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
	m.mux.Lock()
	close(m.stopChan)
	m.mux.Unlock()
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
	m.callback(nil, 10001)
	err = exec.Wait()
	if err != nil {
		logrus.Errorf("%s", err)
		return err
	}
	logrus.Infof("container started")
	return nil
}

func newMonitorCommand(containerId, bashDir string) *exec.Cmd {
	args := []string{
		docker_monitor,
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