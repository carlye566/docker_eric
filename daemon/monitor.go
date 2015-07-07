package daemon

import (
	"sync"
	"time"

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

func (m commonMonitor) StartSignal() chan struct{} {
	return m.startSignal
}