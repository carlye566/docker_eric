package server
import (
	"github.com/gorilla/mux"
	"github.com/Sirupsen/logrus"
	"github.com/docker/docker/pkg/version"
	"github.com/docker/docker/autogen/dockerversion"
	"github.com/docker/docker/daemon"
	"strconv"
	"fmt"
	"net/http"
)

type MonitorServer struct {
	monitor *daemon.DockerMonitor
	Server
}

func NewMonitorServer(cfg *ServerConfig, monitor *daemon.DockerMonitor) *MonitorServer {
	srv := &MonitorServer{
		Server: Server{
			cfg: cfg,
			start: make(chan struct{}),
		},
		monitor: monitor,
	}
	r := createMonitorRouter(srv)
	srv.router = r
	return srv
}

func createMonitorRouter(s *MonitorServer) *mux.Router {
	r := mux.NewRouter()
	m := map[string]map[string]HttpApiFunc{
		"GET": {
			"/_ping":                          s.ping,
		},
		"POST": {
			"/containers/{name:.*}/resize":  s.postContainersResize,
			"/containers/{name:.*}/attach":  s.postContainersAttach,
		},
	}

	for method, routes := range m {
		for route, fct := range routes {
			logrus.Infof("Registering %s, %s", method, route)
			// NOTE: scope issue, make sure the variables are local and won't be changed
			localRoute := route
			localFct := fct
			localMethod := method

			// build the handler function
			f := makeHttpHandler(true, localMethod, localRoute, localFct, "*", version.Version(dockerversion.VERSION))

			// add the new route
			if localRoute == "" {
				r.Methods(localMethod).HandlerFunc(f)
			} else {
				r.Path("/v{version:[0-9.]+}" + localRoute).Methods(localMethod).HandlerFunc(f)
				r.Path(localRoute).Methods(localMethod).HandlerFunc(f)
			}
		}
	}
	return r
}

func (s *MonitorServer) AcceptConnections() {
	// close the lock so the listeners start accepting connections
	select {
	case <-s.start:
	default:
		close(s.start)
	}
}

func (s *MonitorServer) postContainersResize(version version.Version, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	if err := parseForm(r); err != nil {
		return err
	}
	if vars == nil {
		return fmt.Errorf("Missing parameter")
	}

	height, err := strconv.Atoi(r.Form.Get("h"))
	if err != nil {
		return err
	}
	width, err := strconv.Atoi(r.Form.Get("w"))
	if err != nil {
		return err
	}

	return s.monitor.Container().ContainerResize(height, width)
}

func (s *MonitorServer) postContainersAttach(version version.Version, w http.ResponseWriter, r *http.Request, vars map[string]string) error {
	if err := parseForm(r); err != nil {
		return err
	}
	if vars == nil {
		return fmt.Errorf("Missing parameter")
	}

	inStream, outStream, err := hijackServer(w)
	if err != nil {
		return err
	}
	close(s.monitor.WaitAttach)
	logrus.Debugf("hijack complete, closed waitattach")
	defer closeStreams(inStream, outStream)

	if _, ok := r.Header["Upgrade"]; ok {
		fmt.Fprintf(outStream, "HTTP/1.1 101 UPGRADED\r\nContent-Type: application/vnd.docker.raw-stream\r\nConnection: Upgrade\r\nUpgrade: tcp\r\n\r\n")
	} else {
		fmt.Fprintf(outStream, "HTTP/1.1 200 OK\r\nContent-Type: application/vnd.docker.raw-stream\r\n\r\n")
	}

	attachWithLogsConfig := &daemon.ContainerAttachWithLogsConfig{
		InStream:  inStream,
		OutStream: outStream,
		UseStdin:  boolValue(r, "stdin"),
		UseStdout: boolValue(r, "stdout"),
		UseStderr: boolValue(r, "stderr"),
		Logs:      boolValue(r, "logs"),
		Stream:    boolValue(r, "stream"),
	}

	if err := s.monitor.Container().ContainerAttachWithLogs(attachWithLogsConfig); err != nil {
		fmt.Fprintf(outStream, "Error attaching: %s\n", err)
	}

	return nil
}