//go:build !linux

package core

// EnableEnvironmentJobSubreaper is a no-op: this platform has no child subreaper.
func EnableEnvironmentJobSubreaper() {}

// EnvironmentJobDescendantSurvivors is always false: orphans reparent to init here.
func EnvironmentJobDescendantSurvivors(parentPID int, baseline map[int]struct{}) bool { return false }

func EnvironmentJobDescendantBaseline(parentPID int) map[int]struct{} { return nil }
