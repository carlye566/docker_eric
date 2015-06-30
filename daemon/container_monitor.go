package daemon

type containerMonitor interface {
	ExitOnNext()
	Close() error
	Start() error
	StartSignal() chan struct{}
}