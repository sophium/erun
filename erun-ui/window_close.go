package main

import (
	activitysvc "github.com/sophium/erun/erun-ui/internal/activity"
	"github.com/sophium/erun/erun-ui/internal/terminal"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type uiCloseGate struct {
	Blocked bool                `json:"blocked"`
	Running []activitysvc.Entry `json:"running,omitempty"`
}

// PrepareWindowClose is the same decision beforeClose applies, bound so the
// headless Playwright harness can reach it.
func (a *App) PrepareWindowClose() uiCloseGate {
	gate := uiCloseGate{Running: a.activity.Running()}
	gate.Blocked = len(gate.Running) > 0
	if gate.Blocked {
		a.emit(uimodel.AppCloseGateEvent, gate)
	}
	return gate
}

// ConfirmWindowClose persists the interrupted-work record before the quit's
// SIGKILL; a write failure is returned but never blocks the close.
func (a *App) ConfirmWindowClose() error {
	writeErr := a.activity.RecordInterrupted(a.activity.Running())
	a.markCloseConfirmed()
	if a.deps.quitApp != nil {
		a.deps.quitApp()
	} else {
		a.quitDesktopApp()
	}
	return writeErr
}

// markCloseConfirmed lets the next beforeClose pass the running-work gate.
func (a *App) markCloseConfirmed() {
	a.terminal.WithLock(func(*terminal.Locked) {
		a.closeConfirmed = true
	})
}

// consumeCloseConfirmed is one-shot.
func (a *App) consumeCloseConfirmed() bool {
	confirmed := false
	a.terminal.WithLock(func(*terminal.Locked) {
		confirmed = a.closeConfirmed
		a.closeConfirmed = false
	})
	return confirmed
}
