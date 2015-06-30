package daemon
import (
	"time"
	"sync"
	"os"
	"fmt"
	"path/filepath"
	"encoding/json"

	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/docker/runconfig"
)

const (
	container_conf_file_name  = "config.json"
)

type orphanedContainerMonitor struct {
	mux sync.Mutex

	// container is the container being monitored
	container *Container
	// lastStartTime is the time which the monitor last exec'd the container's process
	startTime time.Time
}

func init() {
	log("init container-monitor")
	reexec.Register("container-monitor", reexecMonitor)
}

func reexecMonitor() {
	var (
		containerId = os.Args[1]
		confBaseDir = os.Args[2]
	)
	confDir := filepath.Join(confBaseDir, containerId)
	if config, err := loadConf(filepath.Join(confDir, container_conf_file_name)); err != nil {
		log("Error load config %v", err)
	} else {
		log("success to load %s config, %v", containerId, config)
	}
}

func log(message string, args ...interface{}) {
	fmt.Printf(message+"\n", args...)
	logrus.Printf(message+"\n", args...)
}

func newOrphanedContainerMonitor(container *Container, policy runconfig.RestartPolicy) *orphanedContainerMonitor {
	return &orphanedContainerMonitor{
		container: container,
	}
}

func loadConf(pth string) (*runconfig.Config, error) {
	f, err := os.Open(pth)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var (
		out = &runconfig.Config{}
		dec = json.NewDecoder(f)
	)

	if err := dec.Decode(out); err != nil {
		return nil, err
	}
	return out, nil
}


// Stop signals to the container monitor that it should stop monitoring the container
// for exits the next time the process dies
func (m *orphanedContainerMonitor) ExitOnNext() {
	m.mux.Lock()
	m.mux.Unlock()
}

// Close closes the container's resources such as networking allocations and
// unmounts the contatiner's root filesystem
func (m *orphanedContainerMonitor) Close() error {
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

func (m *orphanedContainerMonitor) Start() error {
	log("container started")
	return nil
}