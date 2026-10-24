//go:build windows

package core

// PlatformProcessZombie cannot answer on Windows, so ProcessAlive uses its own probe.
func PlatformProcessZombie(pid int) (bool, bool) { return false, false }
