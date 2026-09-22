//go:build !linux

package eruncommon

// startEnvironmentJobChildReaper does nothing on a platform where this process
// is not a child subreaper: with no such attribute to declare (see
// enableEnvironmentJobSubreaper), no descendant is ever reparented here, so
// there is no unreaped child for a reaper to find -- and the os/exec children
// this supervisor does own are reaped by their own Wait, as on every platform.
func startEnvironmentJobChildReaper(ownPID int) func() { return func() {} }
