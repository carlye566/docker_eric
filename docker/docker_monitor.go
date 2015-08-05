package main

import (
	apiserver "github.com/docker/docker/api/server"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/Sirupsen/logrus"
	"fmt"
	"os"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/daemon"
)

const (
// TODO this needs to be addressed
	fakeSelf = "/usr/bin/docker"
	docker_monitor = "docker_monitor"
	socketGroup = "docker"
)

func init() {
	reexec.RegisterSelf(docker_monitor, mainDockerMonitor, fakeSelf)
}

func mainDockerMonitor() {
	logrus.Debugf("Starting docker monitor %v", os.Args)
	sockDir := "/var/run/docker/sock"
	if err := os.MkdirAll(sockDir, 0700); err != nil && !os.IsExist(err) {
		logrus.Errorf("can't mkdir %s:%v", sockDir, err)
		os.Exit(1)
	}
	monitor := daemon.InitDockerMonitor()
	setupApiServer([]string {fmt.Sprintf("unix://%s/%s.sock", sockDir, os.Args[1])}, socketGroup, monitor)
	monitor.Start()
}

func setupApiServer(protoAddrs []string, socketGroup string, monitor *daemon.DockerMonitor) {
	api := apiserver.NewMonitorServer(&apiserver.ServerConfig{
		Logging:     true,
		EnableCors:  true,
		//		CorsHeaders: daemonCfg.CorsHeaders,
		Version:     dockerversion.VERSION,
		SocketGroup: socketGroup,
	}, monitor)

	// The serve API routine never exits unless an error occurs
	// We need to start it as a goroutine and wait on it so
	// daemon doesn't exit
	serveAPIWait := make(chan error)
	go func() {
		if err := api.ServeApi(protoAddrs); err != nil {
			logrus.Errorf("ServeAPI error: %v", err)
			serveAPIWait <- err
			return
		}
		serveAPIWait <- nil
	}()
}