//go:build !linux && !windows

package eruncommon

import (
	"fmt"
	"os/exec"
	"strings"
)

// platformProcessZombie answers whether pid names a process that has exited and
// is only waiting to be reaped, from ps's STAT column — the source those
// platforms have, since the /proc table that answers it on Linux does not
// exist there.
//
// The second return is false when ps could not be run or printed nothing for
// that pid, which is not a claim that the process is running: the caller falls
// back to its own probe rather than trusting a default answer.
func platformProcessZombie(pid int) (bool, bool) {
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
