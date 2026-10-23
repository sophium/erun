//go:build !linux

package eruncommon

import "runtime"

// defaultDindEntrypointHost refuses outside Linux: the MTU and cgroup work
// reads a Linux kernel's procfs and sysfs, and the hand-over is an execve.
func defaultDindEntrypointHost() (dindEntrypointHost, error) {
	return dindEntrypointHost{}, dindEntrypointUnsupportedError(runtime.GOOS)
}
