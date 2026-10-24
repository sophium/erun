//go:build linux

package podruntime

import (
	"os"
	"os/exec"
	"syscall"
)

// execve makes dockerd replace erun rather than run as its child.
func defaultDindEntrypointHost() (dindEntrypointHost, error) {
	return dindEntrypointHost{
		procRoot: "/proc",
		sysRoot:  "/sys",
		hostname: os.Hostname,
		lookPath: exec.LookPath,
		exec:     syscall.Exec,
		environ:  os.Environ,
	}, nil
}
