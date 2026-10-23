//go:build !linux

package core

// EnableEnvironmentJobSubreaper does nothing on a platform with no child
// subreaper to declare. There is no way to ask the kernel to keep an orphaned
// descendant accounted to this process, so no reparented descendant is ever
// visible as one, and environmentJobDescendantSurvivors below has nothing to
// find.
func EnableEnvironmentJobSubreaper() {}

// EnvironmentJobDescendantSurvivors always reports false off Linux, because
// enableEnvironmentJobSubreaper above cannot arrange for a descendant to be
// reparented here: the kernel sends an orphan to init instead, which leaves
// nothing naming this supervisor to compare against. The process-group and
// session scans remain the checks those platforms have; this one cannot report
// a survivor it cannot see, and must not report one that is not there.
func EnvironmentJobDescendantSurvivors(parentPID int, baseline map[int]struct{}) bool { return false }

// EnvironmentJobDescendantBaseline has nothing to record: without a subreaper
// to claim orphans there is no descendant for the check above to see, so there
// is no baseline to exclude them by.
func EnvironmentJobDescendantBaseline(parentPID int) map[int]struct{} { return nil }
