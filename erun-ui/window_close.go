package main

import "github.com/sophium/erun/erun-ui/internal/uimodel"

type uiCloseGate struct {
	Blocked bool                 `json:"blocked"`
	Running []activityQueueEntry `json:"running,omitempty"`
}

// runningActivityEntries excludes queued entries and tabs still being
// established, which would otherwise prompt on the idle path after launch.
func (a *App) runningActivityEntries() []activityQueueEntry {
	if a.activityQueue == nil {
		return nil
	}
	var running []activityQueueEntry
	for _, entry := range a.activityQueue.list() {
		if entry.Status == activityQueueStatusRunning && !isGatedSessionSetupEntry(entry) {
			running = append(running, entry)
		}
	}
	return running
}

// PrepareWindowClose is the same decision beforeClose applies, bound so the
// headless Playwright harness can reach it.
func (a *App) PrepareWindowClose() uiCloseGate {
	gate := uiCloseGate{Running: a.runningActivityEntries()}
	gate.Blocked = len(gate.Running) > 0
	if gate.Blocked {
		a.emit(uimodel.AppCloseGateEvent, gate)
	}
	return gate
}

// ConfirmWindowClose persists the interrupted-work record before the quit's
// SIGKILL; a write failure is returned but never blocks the close.
func (a *App) ConfirmWindowClose() error {
	running := a.runningActivityEntries()
	writeErr := writeInterruptedActivityRecord(a.deps.interruptedActivityPath, running)
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
	a.mu.Lock()
	a.closeConfirmed = true
	a.mu.Unlock()
}

// consumeCloseConfirmed is one-shot.
func (a *App) consumeCloseConfirmed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	confirmed := a.closeConfirmed
	a.closeConfirmed = false
	return confirmed
}
