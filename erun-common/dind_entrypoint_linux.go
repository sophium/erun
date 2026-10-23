//go:build linux

package eruncommon

import (
	"os"
	"os/exec"
	"syscall"
)

// defaultDindEntrypointHost wires the sidecar's real procfs, sysfs and
// hostname, and hands the process over to dockerd-entrypoint.sh with execve,
// so the daemon replaces erun rather than running as its child.
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
