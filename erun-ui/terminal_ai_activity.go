package main

import (
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// EndAISessions permanently ends the env's AI sessions — the env AI tab and
// the contribute AI tab, desktop side and pod side — so the next
// StartAISession / StartContributeAISession launches Claude with the env's
// current launch flags (--effort / --model / --verbose --debug). A launch
// flag only takes effect when the persistent session's
// create-time program runs: `dtach -A` reattaches to the running claude, so
// without ending the pod session a changed flag could never apply. The pod
// sessions are ended even when no desktop tab is attached — a detached claude
// would otherwise keep its stale flags and the next open would silently
// reattach to it. The relaunched guard resumes via --continue, so the Claude
// conversation carries over. Local/ERun tabs are untouched: their launch
// command does not change.
//
// Returns false without ending anything when the env's AI tool launches
// verbatim (a non-claude tool, or claude invoked with explicit flags): the
// managed Claude launch flags never participate in that launch, and ending
// would discard a session — codex has no --continue — for a setting that
// cannot affect it.
func (a *App) EndAISessions(selection uiSelection) (bool, error) {
	selection = normalizeSelection(selection)
	if err := errMissingTenantOrEnvironment("end AI sessions", selection.Tenant, selection.Environment); err != nil {
		return false, err
	}
	if envConfig, _, err := a.deps.store.LoadEnvConfig(selection.Tenant, selection.Environment); err == nil {
		// A root config that cannot be read means "no gateway configured",
		// matching how a missing env config is tolerated just above: this only
		// decides whether the launch is verbatim, and the unreadable case is the
		// unconfigured one.
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
		// Mark closed under the lock so the spawn-reuse branch and
		// tryReconnect both refuse this session while it is torn down; the
		// frontend respawn that follows must create a fresh session, not
		// reattach to the dying one.
		markClosedByTeardownLocked(managed)
		a.releaseIdleBlockLocked(managed)
		targets = append(targets, managed)
	}
	a.mu.Unlock()
	for _, managed := range targets {
		a.withdrawExitOwnership(managed, managed.session.Close())
	}
	// End the pod-side persistent sessions only after the desktop PTYs are
	// gone: ending first would remove the owner file under an attached
	// client, which exits with the taken-over code and freezes the tab on
	// the takeover marker instead of relaunching.
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

// aiActivitySustainedThreshold is how long the AI session must keep
// producing output before we flip the sidebar busy badge on. Chosen
// large enough to suppress single-line Codex responses (~1 s of output)
// while still catching real multi-second Claude generations.
const aiActivitySustainedThreshold = 5 * time.Second

// aiActivityIdleThreshold is how long the AI session must be silent
// before we flip the busy badge back off. Codex's "thinking..." spinner
// updates in bursts; this window swallows the gaps between bursts so
// the sidebar does not flicker mid-generation.
const aiActivityIdleThreshold = 3 * time.Second

// recordAIActivity is called from streamSession on every output read for
// every managed terminal; it only does work for sessionKindAI sessions.
// It implements the debounced "AI tab is working" signal described in
// erun-ui/AGENTS.md: Nielsen #1 (visibility of system status) requires
// the sidebar to show, at a glance, which env's AI tab is producing
// output — even when the user has navigated to a different env.
//
// This is the fallback half of that signal, for a session whose AI tool has
// never reported a turn boundary; where the tool does report one, the state it
// reports is the answer and this volume rule stands down (ai_session_status.go).
//
// Policy:
//   - busy=true fires after aiActivitySustainedThreshold of sustained
//     output (5 s), where "sustained" means continued output with gaps
//     no longer than aiActivityIdleThreshold (3 s). Short single-burst
//     responses do not toggle the badge.
//   - busy=false fires after aiActivityIdleThreshold (3 s) of silence.
//   - Session close emits busy=false via finalizeAIActivity.
//
// aiActivityKind reports whether a session of this kind should drive the
// sidebar's working spinner. An orchestrator runs the same agent an AI tab
// does, and it is the row most likely to be working, so gating this on
// sessionKindAI alone left the one row that is always driving work as the only
// row that never showed it.
//
// It takes the kind rather than the session so each caller keeps its own
// `managed == nil` guard visible: folding the nil check in here hid it from
// static analysis, which then read every later field access as a possible nil
// dereference.
//
// An orchestrator is deliberately NOT here. It runs an interactive agent TUI,
// which repaints its prompt and counters continuously, so "this terminal is
// emitting bytes" is true forever and the silence rule that releases the latch
// never fires — the row span forever and the desktop burned CPU reading redraws
// to keep it that way. An env's AI tab survives the same weakness only because
// the pod heartbeat independently observes its program exit. An orchestrator has
// no pod, so it reports its own turn boundaries instead (orchestrator_activity.go).
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
	// Where the tool reports its own state, the volume rule stands down
	// entirely — not only for the release but for the latch too. A tool that is
	// waiting on the human can still repaint its prompt, and letting those
	// redraws raise the badge would set it oscillating against the report that
	// keeps clearing it. The poller owns both directions there.
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

// scheduleAIQuietCheckLocked (re)arms the timer that decides whether this
// session's busy latch has gone quiet. Arming it on every read is the debounce:
// a read resets it, so the firing that matters is always the one 3 s after the
// last output. Caller holds a.mu.
func (a *App) scheduleAIQuietCheckLocked(managed *managedTerminal) {
	if managed.aiInactivityTimer != nil {
		managed.aiInactivityTimer.Stop()
	}
	managed.aiInactivityTimer = time.AfterFunc(aiActivityIdleThreshold, func() {
		a.clearAIActivityIfQuiet(managed)
	})
}

// clearAIActivityIfQuiet fires from recordAIActivity's AfterFunc and clears the
// busy latch only if the session has stayed quiet. If newer output arrived, a
// later recordAIActivity already reset the timer, so this firing is stale and a
// no-op.
//
// Silence alone is not enough to declare the work finished: an agent waiting on
// a compile, and a session whose output stream dropped, are both silent while
// the program in the pod keeps running. So a session the pod recently observed
// as running keeps the latch — the heartbeat poller releases it when the pod
// agrees it has stopped (applySessionHeartbeat).
func (a *App) clearAIActivityIfQuiet(managed *managedTerminal) {
	if managed == nil {
		return
	}
	// The tool's own report answers this question directly when it has one, and
	// it is the only evidence that separates "finished its turn and waiting on
	// you" from "still working": both are silent, and the pod's liveness is the
	// same answer for both. See ai_session_status.go.
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

// releaseAIActivity drops the busy latch regardless of how recently the session
// printed. Used when the pod itself reports the session's program is gone: the
// stream may still be dribbling reconnect noise, but the work is over.
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

// latchAIActivity raises the busy latch for a session the AI tool itself
// reports as working, without the five seconds of output volume the heuristic
// needs: the tool said its turn started, which is the thing the volume rule was
// approximating. The latch is marked as the tool's own, so pod liveness alone
// never holds it open — it is re-asserted by the next reading, and released
// when the tool stops saying it (see releaseUnobservedAIActivity).
//
// Caller must not hold a.mu.
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

// finalizeAIActivity ensures the sidebar busy latch is released when an
// AI session exits while busy=true is in flight (e.g. user closes the
// tab mid-generation, or the underlying PTY drops). Caller must not
// hold a.mu.
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
