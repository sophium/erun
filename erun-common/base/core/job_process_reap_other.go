//go:build !linux

package core

// StartEnvironmentJobChildReaper is a no-op where this process is not a subreaper.
func StartEnvironmentJobChildReaper(ownPID int) func() { return func() {} }
