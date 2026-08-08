package main

import (
	"strings"
	"time"
)

// session_heartbeat.go is the live-but-quiet half of session reconciliation.
// activity_stale_sessions.go already handles the opposite direction — a shell
// that died without closing its pty becomes visible in the activity queue. This
// side handles a session that is very much alive but has stopped printing.
//
// The AI busy signal used to be a pure function of stream traffic: five seconds
// of output latched it on, three seconds of silence latched it off. Silence is
// not evidence of finishing, though. An agent waiting on a compile, a dtach
// client detached by another window, a kubectl exec stream that dropped — all
// look identical to "done" from the stream alone, so the tab could report the
// work finished while the pod was still crunching, and a rendered session count
// taken from a different source could disagree with it.
//
// So the busy latch is now released only when the pod agrees the session has
// stopped, and released immediately when the pod says the program is gone. What
// the UI animates and what it counts are the same observation.

// sessionHeartbeatInterval paces the pod probe. Long enough that an idle
// desktop is not execing into every open environment every few seconds, short
// enough that a finished agent turn stops reading as running promptly.
const sessionHeartbeatInterval = 15 * time.Second

// sessionHeartbeatTTL bounds how long an observation stays authoritative. Past
// it the observation is treated as absent and the stream-silence rule decides
// again, so an unreachable pod can never latch a session as "running forever".
const sessionHeartbeatTTL = 3 * sessionHeartbeatInterval

// sessionHeartbeat is one environment's most recent observation.
type sessionHeartbeat struct {
	observedAt time.Time
	// running holds the app-session ids the pod reported with a live program.
	running map[string]struct{}
	// sessions is every socket the pod reported, running or not, so a session
	// the pod does not know about at all is distinguishable from one it reports
	// as stopped.
	sessions map[string]struct{}
}

func (a *App) runSessionHeartbeatPoller(stop <-chan struct{}) {
	ticker := time.NewTicker(sessionHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			a.reconcileSessionHeartbeatsOnce()
			// Mirrors are re-enumerated here, not only at boot. An env linked
			// while the app is already running — the usual case, since linking is
			// done from the running app — never got a sync worker, so its mirror
			// stayed silently empty until the next restart, indistinguishable
			// from an env with no files. startWorkspaceSyncForSelection
			// re-validates and dedups a running poller, so this settles to a
			// no-op once every linked env is syncing.
			a.reconcileWorkspaceSyncForConfiguredEnvs()
		}
	}
}

// reconcileSessionHeartbeatsOnce probes every environment with a live pod
// session and applies the result to the sessions that environment owns.
func (a *App) reconcileSessionHeartbeatsOnce() {
	for _, selection := range a.selectionsWithPodSessions() {
		activity, err := a.probeRuntimeActivity(selection)
		if err != nil {
			// An unreadable pod is a diagnostic problem, not a finished
			// session: leave the previous observation to age out of its TTL
			// rather than declaring everything stopped.
			continue
		}
		a.applySessionHeartbeat(selection, activity)
	}
	a.releaseUnobservedAIActivity()
}

// releaseUnobservedAIActivity is the safety valve for the observation going
// away entirely — an environment stopped, its pod replaced, the cluster
// unreachable. Without it a latch held open by a since-expired observation
// would never be released, because the silence timer that would have cleared it
// already fired once and was declined.
func (a *App) releaseUnobservedAIActivity() {
	a.mu.Lock()
	var candidates []*managedTerminal
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || !aiActivityKind(managed.kind) || !managed.aiBusyEmitted {
			continue
		}
		heartbeat, ok := a.sessionHeartbeats[selectionKey(managed.selection)]
		if ok && time.Since(heartbeat.observedAt) <= sessionHeartbeatTTL {
			continue
		}
		candidates = append(candidates, managed)
	}
	a.mu.Unlock()
	for _, managed := range candidates {
		a.releaseAIActivityIfQuiet(managed)
	}
}

func (a *App) selectionsWithPodSessions() []uiSelection {
	seen := make(map[string]struct{})
	var out []uiSelection
	a.mu.Lock()
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || strings.TrimSpace(managed.appSession) == "" {
			continue
		}
		key := selectionKey(managed.selection)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, managed.selection)
	}
	a.mu.Unlock()
	return out
}

// applySessionHeartbeat stores the observation and releases the busy latch of
// every AI session the pod reports as no longer running. The reverse direction
// needs no action here: heartbeatSaysRunning simply stops the silence timer
// from clearing a session the pod still reports as alive.
func (a *App) applySessionHeartbeat(selection uiSelection, activity uiRuntimeActivity) {
	heartbeat := sessionHeartbeat{
		observedAt: time.Now(),
		running:    make(map[string]struct{}, len(activity.Sessions)),
		sessions:   make(map[string]struct{}, len(activity.Sessions)),
	}
	for _, session := range activity.Sessions {
		heartbeat.sessions[session.ID] = struct{}{}
		if session.Running {
			heartbeat.running[session.ID] = struct{}{}
		}
	}

	key := selectionKey(selection)
	a.mu.Lock()
	if a.sessionHeartbeats == nil {
		a.sessionHeartbeats = make(map[string]sessionHeartbeat)
	}
	a.sessionHeartbeats[key] = heartbeat
	var finished []*managedTerminal
	for _, managed := range a.sessions {
		if heartbeat.reportsFinished(managed, key) {
			finished = append(finished, managed)
		}
	}
	a.mu.Unlock()

	for _, managed := range finished {
		a.releaseAIActivity(managed)
	}
}

// reportsFinished answers "this observation says that session's program is
// gone". A session the observation does not mention at all is not an answer —
// the pod may simply not have created its socket yet — so it is left alone.
// Caller holds a.mu.
func (h sessionHeartbeat) reportsFinished(managed *managedTerminal, key string) bool {
	if managed == nil || managed.closed || !aiActivityKind(managed.kind) {
		return false
	}
	if selectionKey(managed.selection) != key || !managed.aiBusyEmitted {
		return false
	}
	id := strings.TrimSpace(managed.appSession)
	if _, known := h.sessions[id]; !known {
		return false
	}
	_, running := h.running[id]
	return !running
}

// heartbeatSaysRunning reports whether the pod recently observed this session
// with a live program. A missing, expired, or session-unaware observation
// answers false so the caller falls back to the stream-silence rule it always
// used — the heartbeat can only keep a session marked running, never invent one.
func (a *App) heartbeatSaysRunning(managed *managedTerminal) bool {
	if managed == nil {
		return false
	}
	id := strings.TrimSpace(managed.appSession)
	if id == "" {
		return false
	}
	a.mu.Lock()
	heartbeat, ok := a.sessionHeartbeats[selectionKey(managed.selection)]
	a.mu.Unlock()
	if !ok || time.Since(heartbeat.observedAt) > sessionHeartbeatTTL {
		return false
	}
	_, running := heartbeat.running[id]
	return running
}

// forgetSessionHeartbeats drops an environment's observation when its sessions
// go away, so a reopened environment starts from a fresh reading rather than
// one taken before the pod was replaced.
func (a *App) forgetSessionHeartbeats(selection uiSelection) {
	key := selectionKey(selection)
	a.mu.Lock()
	delete(a.sessionHeartbeats, key)
	a.mu.Unlock()
}
