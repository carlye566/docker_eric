package main

import (
	"strings"
	"net"
	"os/exec"

	"github.com/go-check/check"
	"github.com/docker/libnetwork/netutils"
)

func (s *DockerSuite) TestEnvironmentGAIA_HOST_IP(c *check.C) {
	runCmd := exec.Command(dockerBinary, "run", "-d", "busybox", "top")
        out, _, _, err := runCommandWithStdoutStderr(runCmd)
        if err != nil {
                c.Fatalf("failed to run container: %v, output: %q", err, out)
        }
        containerID := strings.TrimSpace(out)

	runCmd = exec.Command(dockerBinary, "exec", "-i", containerID, "env")
        out, _, _, err = runCommandWithStdoutStderr(runCmd)
        if err != nil {
                c.Fatalf("failed to exec container: %s, error: %v", containerID, err)
        }

        V_GAIA_HOST_IP := "GAIA_HOST_IP=" + getLocalAddr()
	if ok := strings.Contains(strings.TrimSpace(out), V_GAIA_HOST_IP); !ok {
		c.Fatalf("could not find GAIA_HOST_IP from env: %s", strings.TrimSpace(out))
	}
}

func getLocalAddr() string {
	iLists := []string { "eth1", "eth0" } 
        for _, iface := range iLists {
                addrv4, _, err := netutils.GetIfaceAddr(iface)
                if err == nil {
                        return addrv4.(*net.IPNet).IP.String()
                }
        }
	return ""
}
