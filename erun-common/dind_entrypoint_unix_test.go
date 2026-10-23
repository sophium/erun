//go:build !windows

package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// A write the kernel accepts is not proof the quota is in force: a cpu.max
// that does not read back the mirrored value is reported rather than
// announced. /dev/null accepts the write and reads back empty.
func TestDindEntrypointReportsACapThatDoesNotReadBack(t *testing.T) {
	run := newDindRun(t)
	run.stubNet(t, "eth0", "1450")
	run.stubOwnCgroup(t, "400000 100000")
	run.stubCapParent(t, "cpuset cpu io memory pids", "cpu")
	mustMkdirAll(t, run.capCgroup())
	if err := os.Symlink(os.DevNull, filepath.Join(run.capCgroup(), "cpu.max")); err != nil {
		t.Fatal(err)
	}
	run.run(t, false)
	run.assertUncappedReported(t, "reads back '', not '400000 100000'")
}
