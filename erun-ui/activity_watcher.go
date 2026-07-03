package main

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// isProcessAliveOrDefault treats a non-positive PID as alive because liveness
// is then unknown and the caller's other signals decide. The stale-shell
// detector uses it to flag PTY children that exited without the desktop noticing.
func isProcessAliveOrDefault(pid int) bool {
	if pid <= 0 {
		return true
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	signalErr := proc.Signal(syscall.Signal(0))
	if signalErr == nil {
		return true
	}
	if errors.Is(signalErr, syscall.ESRCH) {
		return false
	}
	return true
}

func (a *App) activityWatcherCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
