// +build linux

package native

import (
	"fmt"
	"os"
	"runtime"

	"github.com/docker/docker/pkg/reexec"
	"github.com/docker/libcontainer"
	"github.com/docker/docker/vendor/src/github.com/Sirupsen/logrus"
)

func init() {
	reexec.Register(DriverName, initializer)
}

func fatal(err error) {
	if lerr, ok := err.(libcontainer.Error); ok {
		lerr.Detail(os.Stderr)
		os.Exit(1)
	}

	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func initializer() {
	runtime.GOMAXPROCS(1)
	runtime.LockOSThread()
	logrus.Infof("I'm in container")
	factory, err := libcontainer.New("")
	if err != nil {
		fatal(err)
	}
	if err := factory.StartInitialization(); err != nil {
		fatal(err)
	}

	panic("unreachable")
}

func writeError(err error) {
	fmt.Fprint(os.Stderr, err)
	os.Exit(1)
}
