package main

// uiCloseGate is what the frontend receives when the operator tries to close
// the window. Blocked names the build/deploy/release work that is actually
// in flight right now, so the confirmation the operator sees is concrete
// rather than a generic warning.
type uiCloseGate struct {
	Blocked bool                 `json:"blocked"`
	Running []activityQueueEntry `json:"running,omitempty"`
}

// runningActivityEntries returns the activity queue entries that are
// currently executing — the work a SIGKILL-on-close would tear down mid-flight.
// Waiting (queued but not yet started) entries are excluded: nothing has run
// for them yet, so closing loses no partial work.
func (a *App) runningActivityEntries() []activityQueueEntry {
	if a.activityQueue == nil {
		return nil
	}
	var running []activityQueueEntry
	for _, entry := range a.activityQueue.list() {
		if entry.Status == activityQueueStatusRunning {
			running = append(running, entry)
		}
	}
	return running
}

// PrepareWindowClose evaluates whether the window may close right now. It is
// the same decision beforeClose applies to the native OS close button,
// exposed as its own bound method so the confirmation flow — and
// ConfirmWindowClose's re-check — exercise one code path, and so the flow is
// reachable from the headless Playwright harness, which has no real window to
// close. When work is running it also posts the gate as an event so a
// re-open of the dialog (e.g. a second close attempt) sees the current list.
func (a *App) PrepareWindowClose() uiCloseGate {
	gate := uiCloseGate{Running: a.runningActivityEntries()}
	gate.Blocked = len(gate.Running) > 0
	if gate.Blocked {
		a.emit(appCloseGateEvent, gate)
	}
	return gate
}

// ConfirmWindowClose is the operator's explicit choice, from the close
// confirmation, to close despite the running work PrepareWindowClose named.
// It records what is being interrupted before anything is killed — the
// SIGKILL that follows destroys the process and the in-memory activity queue
// in the same instant, so the record has to land first — then asks Wails to
// quit, which drives beforeClose/shutdown down the normal path. A failure to
// persist the record is reported but never blocks the close the operator
// already confirmed: the record is a best-effort courtesy to the next
// launch, not a precondition for honoring "close anyway".
func (a *App) ConfirmWindowClose() error {
	running := a.runningActivityEntries()
	writeErr := writeInterruptedActivityRecord(a.deps.interruptedActivityPath, running)
	a.mu.Lock()
	a.closeConfirmed = true
	a.mu.Unlock()
	if a.deps.quitApp != nil {
		a.deps.quitApp()
	} else {
		a.quitDesktopApp()
	}
	return writeErr
}

// consumeCloseConfirmed reports and clears the operator's prior "close
// anyway" choice, so a single confirmation lets the Quit-triggered
// beforeClose pass through exactly once rather than prompting again.
func (a *App) consumeCloseConfirmed() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	confirmed := a.closeConfirmed
	a.closeConfirmed = false
	return confirmed
}
