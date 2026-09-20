//go:build !linux

package eruncommon

// enableEnvironmentJobSubreaper does nothing on a platform with no child
// subreaper to declare. There is no way to ask the kernel to keep an orphaned
// descendant accounted to this process, so no reparented descendant is ever
// visible as one, and environmentJobDescendantSurvivors below has nothing to
// find.
func enableEnvironmentJobSubreaper() {}

// environmentJobDescendantSurvivors always reports false off Linux, because
// enableEnvironmentJobSubreaper above cannot arrange for a descendant to be
// reparented here: the kernel sends an orphan to init instead, which leaves
// nothing naming this supervisor to compare against. The process-group and
// session scans remain the checks those platforms have; this one cannot report
// a survivor it cannot see, and must not report one that is not there.
func environmentJobDescendantSurvivors(parentPID int) bool { return false }
