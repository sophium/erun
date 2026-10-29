//go:build linux

package podruntime

import (
	"testing"
)

func TestDindEntrypointHostReadsTheKernelsOwnProcfsAndSysfs(t *testing.T) {
	host, err := defaultDindEntrypointHost()
	if err != nil {
		t.Fatal(err)
	}
	if host.procRoot != "/proc" || host.sysRoot != "/sys" {
		t.Fatalf("host roots = %q, %q; want /proc and /sys", host.procRoot, host.sysRoot)
	}
	if host.hostname == nil || host.lookPath == nil || host.exec == nil || host.environ == nil {
		t.Fatal("every host seam must be wired on Linux")
	}
}
