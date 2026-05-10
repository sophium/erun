package main

import (
	"context"
	"errors"
	"os"
	"syscall"
)

// isProcessAliveOrDefault returns true when the supplied PID is currently
// running. PID 0 / negative is treated as alive (we don't know — defer to
// the caller's other liveness signals). Used by the stale-shell detector
// to flag PTY children that exited without the desktop noticing.
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

// activityWatcherCtx returns a cancellable context derived from the app
// context if one is available, or context.Background otherwise. Used by
// the helm-release poller and stale-shell detector when they spawn
// ad-hoc kubectl/helm subprocesses.
func (a *App) activityWatcherCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}
