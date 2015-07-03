package daemon

import (
	"sync"
	"time"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/daemon/execdriver"
	"github.com/docker/docker/runconfig"
)

type containerMonitor interface {
	ExitOnNext()
	Close() error
	Start() error
	StartSignal() chan struct{}
}

type commonMonitor struct {
	mux sync.Mutex

	// container is the container being monitored
	container *Container

	// restartPolicy is the current policy being applied to the container monitor
	restartPolicy runconfig.RestartPolicy

	// startSignal is a channel that is closes after the container initially starts
	startSignal chan struct{}

	// stopChan is used to signal to the monitor whenever there is a wait for the
	// next restart so that the timeIncrement is not honored and the user is not
	// left waiting for nothing to happen during this time
	stopChan chan struct{}
	// lastStartTime is the time which the monitor last exec'd the container's process
	startTime time.Time
}

// callback ensures that the container's state is properly updated after we
// received ack from the execution drivers
func (m commonMonitor) callback(processConfig *execdriver.ProcessConfig, pid int) {
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

func (m commonMonitor) StartSignal() chan struct{} {
	return m.startSignal
}