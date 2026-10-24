package main

import (
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// EndAISessions ends the env's AI sessions, desktop and pod side, so the next
// launch picks up changed Claude flags: `dtach -A` would otherwise reattach to
// the stale process. Returns false without ending anything when the AI tool
// launches verbatim, since the managed flags cannot affect it.
func (a *App) EndAISessions(selection uiSelection) (bool, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("end AI sessions", selection.Tenant, selection.Environment); err != nil {
		return false, err
	}
	if envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment); err == nil {
		// An unreadable root config is treated as no gateway configured.
		var gateway *configpkg.OpenRouterConfig
		if config, _, err := a.deps.store.LoadERunConfig(); err == nil {
			gateway = config.OpenRouter
		}
		launch := open.AISessionLaunchCommand(envConfig.AITool, envConfig.Claude, gateway, selection.Tenant, selection.Environment)
		if launch == strings.TrimSpace(envConfig.AITool) {
			return false, nil
		}
	}
	var targets []*managedTerminal
	a.mu.Lock()
	for _, managed := range a.sessions {
		if !managedAITabFor(managed, selection) {
			continue
		}
		// Marked closed so the frontend respawn creates a fresh session instead of
		// reattaching to this one.
		markClosedByTeardownLocked(managed)
		a.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	a.mu.Unlock()
	for _, managed := range targets {
		a.withdrawExitOwnership(managed, managed.session.Close())
	}
	// End pod sessions only after the desktop PTYs are gone, or the attached client
	// exits with the taken-over code and freezes the tab.
	var wg sync.WaitGroup
	for _, sessionID := range []string{"ai", "contribute-ai"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			a.endRemoteAppSession(selection, id)
		}(sessionID)
	}
	wg.Wait()
	return true, nil
}

func managedAITabFor(managed *managedTerminal, selection uiSelection) bool {
	if managed == nil || managed.closed || managed.session == nil {
		return false
	}
	if managed.selection.Tenant != selection.Tenant ||
		managed.selection.Environment != selection.Environment {
		return false
	}
	return managed.kind == sessionKindAI || managed.kind == sessionKindContributeAI
}

// aiActivitySustainedThreshold suppresses single-line responses.
const aiActivitySustainedThreshold = 5 * time.Second

// aiActivityIdleThreshold swallows gaps between spinner bursts.
const aiActivityIdleThreshold = 3 * time.Second

// aiActivityKind reports whether a session kind drives the sidebar's volume-based
// working spinner. Orchestrators are excluded: their TUI repaints forever, so
// silence never arrives; they report turn boundaries instead.
// It takes the kind so each caller's own nil guard stays visible to static analysis.
func aiActivityKind(kind sessionKind) bool {
	return kind == sessionKindAI
}

func (a *App) recordAIActivity(managed *managedTerminal) {
	if managed == nil || !aiActivityKind(managed.kind) {
		return
	}
	now := time.Now()
	a.mu.Lock()
	if managed.closed {
		a.mu.Unlock()
		return
	}
	// Where the tool reports its own state it owns both latch and release; prompt
	// repaints would otherwise make the badge oscillate.
	if _, reported := a.aiSessionEvidenceForTabLocked(managed); reported {
		a.mu.Unlock()
		return
	}
	if managed.aiActiveSince.IsZero() || now.Sub(managed.aiLastOutput) > aiActivityIdleThreshold {
		managed.aiActiveSince = now
	}
	managed.aiLastOutput = now
	shouldFireBusy := !managed.aiBusyEmitted && now.Sub(managed.aiActiveSince) >= aiActivitySustainedThreshold
	if shouldFireBusy {
		managed.aiBusyEmitted = true
	}
	a.scheduleAIQuietCheckLocked(managed)
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	if shouldFireBusy {
		a.emitAIActivity(serial, selection, true)
	}
}

// scheduleAIQuietCheckLocked re-arms the quiet timer on every read; that is the
// debounce. Caller holds a.mu.
func (a *App) scheduleAIQuietCheckLocked(managed *managedTerminal) {
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
	}
	managed.aiInactivityTimer = time.AfterFunc(aiActivityIdleThreshold, func() {
		a.clearAIActivityIfQuiet(managed)
	})
}

// clearAIActivityIfQuiet is a no-op when newer output re-armed the timer. A
// session the pod still sees running keeps the latch, since silence does not
// mean finished.
func (a *App) clearAIActivityIfQuiet(managed *managedTerminal) {
	if managed == nil {
		return
	}
	// Only the tool's report separates finished-and-waiting from still-working.
	if busy, reported := a.aiSessionEvidenceForTab(managed); reported {
		if busy {
			return
		}
		a.releaseAIActivity(managed)
		return
	}
	if a.heartbeatSaysRunning(managed) {
		return
	}
	a.releaseAIActivityIfQuiet(managed)
}

func (a *App) releaseAIActivityIfQuiet(managed *managedTerminal) {
	a.mu.Lock()
	if managed.closed || !managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	if time.Since(managed.aiLastOutput) < aiActivityIdleThreshold {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, false)
}

// releaseAIActivity drops the latch when the pod reports the program gone.
func (a *App) releaseAIActivity(managed *managedTerminal) {
	a.mu.Lock()
	if managed.closed || !managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, false)
}

// latchAIActivity raises the latch on the tool's own report, which pod liveness
// alone never holds open. Caller must not hold a.mu.
func (a *App) latchAIActivity(managed *managedTerminal) {
	if managed == nil || !aiActivityKind(managed.kind) {
		return
	}
	a.mu.Lock()
	if managed.closed || managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = true
	managed.aiModelLatch = true
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, true)
}

// finalizeAIActivity releases the busy latch when an AI session exits. Caller
// must not hold a.mu.
func (a *App) finalizeAIActivity(managed *managedTerminal) {
	if managed == nil || !aiActivityKind(managed.kind) {
		return
	}
	a.mu.Lock()
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
		managed.aiInactivityTimer = nil
	}
	if !managed.aiBusyEmitted {
		a.mu.Unlock()
		return
	}
	managed.aiBusyEmitted = false
	managed.aiActiveSince = time.Time{}
	managed.aiModelLatch = false
	selection := managed.selection
	serial := managed.serial
	a.mu.Unlock()
	a.emitAIActivity(serial, selection, false)
}

func (a *App) emitAIActivity(sessionID int, selection uiSelection, busy bool) {
	a.emitEvent(aiActivityEvent, aiActivityPayload{
		SessionID:   sessionID,
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
		Busy:        busy,
	})
}
