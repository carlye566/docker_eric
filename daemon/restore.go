package daemon
import "fmt"

func (daemon *Daemon) containerRestore(container *Container) {
	container.Lock()
	defer container.Unlock()

	if err, stopStatus := container.loadStopStatus(); err == nil {
		container.setStopped(&stopStatus.ExitStatus)
		return
	}
	if err, startStatus := container.loadStartStatus(); err == nil {
		if startStatus.Err != "" {
			container.setError(fmt.Errorf(startStatus.Err))
		} else {
			container.setRunning(startStatus.Pid)
		}
	}
}