//go:build !linux && !windows

package core

import (
	"fmt"
	"os/exec"
	"strings"
)

// PlatformProcessZombie reports whether pid is a zombie, from ps's STAT column.
// The second return is false when that cannot be determined.
func PlatformProcessZombie(pid int) (bool, bool) {
	if pid <= 0 {
		return false, false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", fmt.Sprintf("%d", pid)).Output()
	if err != nil {
		return false, false
	}
	stat := strings.TrimSpace(string(out))
	if stat == "" {
		return false, false
	}
	return psStatIsZombie(stat), true
}
