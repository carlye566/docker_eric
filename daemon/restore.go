package daemon

func (daemon *Daemon) containerRestore(container *Container) {
	container.Lock()
	defer container.Unlock()

	if err, stopStatus := container.loadStopStatus(); err == nil {
		container.setStopped(&stopStatus.ExitStatus)
		return
	}

	container.monitor = newExternalMonitor(container, container.hostConfig.RestartPolicy)
}