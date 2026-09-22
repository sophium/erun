//go:build windows

package eruncommon

// platformProcessZombie has no answer to give on Windows: there is no zombie
// state to distinguish there, and no process table this package can read for
// one. Reporting "could not answer" leaves ProcessAlive on its own probe, which
// is the whole answer that platform has always used.
func platformProcessZombie(pid int) (bool, bool) { return false, false }
