package main

import (
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Stream silence is not evidence an AI session finished, so the busy latch is
// released only when a pod heartbeat says the program stopped.

const sessionHeartbeatInterval = 15 * time.Second

// Past sessionHeartbeatTTL the stream-silence rule decides again, so an unreachable
// pod can never latch a session as running forever.
const sessionHeartbeatTTL = 3 * sessionHeartbeatInterval

type sessionHeartbeat struct {
	observedAt time.Time
	running    map[string]struct{}
	// sessions includes stopped ones, so "unknown to the pod" differs from "stopped".
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
			// After the heartbeats, so this tick's pod view is in hand.
			a.reconcileAISessionStatusesOnce()
			// Re-enumerated every tick: an env linked while the app runs gets no sync worker
			// otherwise. Idempotent once every linked env is syncing.
			a.reconcileWorkspaceSyncForConfiguredEnvs()
			a.reconcileOrchestratorActivity()
			// After reconcileOrchestratorActivity so shellRunning is this tick's report.
			a.reconcileOrchestratorPacing()
			// Reaps reports for orchestrator ids no longer in a.orchestrators.
			a.reconcileOrphanedOrchestratorShellActivity(time.Now())
		}
	}
}

// reconcileOrchestratorActivity re-emits every tick, not only on change, so a
// missed event self-heals; session.aiBusy also feeds orchestratorInfo.Busy for snapshots.
func (a *App) reconcileOrchestratorActivity() {
	now := time.Now()
	a.mu.Lock()
	type row struct {
		id      string
		serial  int
		alive   bool
		agentID string
	}
	rows := make([]row, 0, len(a.orchestrators))
	for id, session := range a.orchestrators {
		if session == nil || session.transient {
			continue
		}
		// A session we can no longer see ages out on the short bound, since nothing else will clear it.
		managed := a.sessions[orchestratorSessionKey(id)]
		rows = append(rows, row{
			id:      id,
			serial:  session.serial,
			alive:   managed != nil && !managed.closed,
			agentID: session.agentID,
		})
	}
	a.mu.Unlock()
	conversations := a.orchestratorAgentConversations(len(rows) > 0)

	for _, r := range rows {
		activity, ok := readOrchestratorActivity(r.id, now, r.alive)
		busy := ok && activity.Busy
		// The shell report is independent of turn busy state, and must name the session
		// that wrote it: sessionAlive is per orchestrator id, so a replaced session's
		// report would otherwise borrow its successor's liveness.
		liveConversationID := conversations[r.agentID]
		shell, shellOK := readOrchestratorShellActivity(r.id, now, r.alive, liveConversationID)
		shellRunning := shellOK && shell.Running
		a.mu.Lock()
		if session := a.orchestrators[r.id]; session != nil {
			session.aiBusy = busy
			session.aiBusyAtUnix = activity.AtUnix
			session.shellRunning = shellRunning
			session.shellCommand = shell.Command
			session.shellStartedAtUnix = shell.AtUnix
		}
		a.mu.Unlock()
		a.emitAIActivity(r.serial, uimodel.Selection{}, busy)
		a.emitOrchestratorShellActivity(r.serial, shellRunning, shell.Command, shell.AtUnix)
	}
}

func (a *App) emitOrchestratorShellActivity(sessionID int, running bool, command string, startedAtUnix int64) {
	a.emitEvent(uimodel.OrchestratorShellEvent, orchestratorShellActivityPayload{
		SessionID:     sessionID,
		Running:       running,
		Command:       command,
		StartedAtUnix: startedAtUnix,
	})
}

func (a *App) reconcileSessionHeartbeatsOnce() {
	for _, selection := range a.selectionsWithPodSessions() {
		activity, err := a.probeRuntimeActivity(selection)
		if err != nil {
			// An unreadable pod is not a finished session; let the observation age out.
			continue
		}
		a.applySessionHeartbeat(selection, activity)
	}
	a.releaseUnobservedAIActivity()
}

// releaseUnobservedAIActivity releases latches whose observation expired; the
// silence timer that would have cleared them already fired and was declined.
func (a *App) releaseUnobservedAIActivity() {
	a.mu.Lock()
	var candidates []*managedTerminal
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || !aiActivityKind(managed.kind) || !managed.aiBusyEmitted {
			continue
		}
		if a.aiLatchStillSupportedLocked(managed) {
			continue
		}
		candidates = append(candidates, managed)
	}
	a.mu.Unlock()
	for _, managed := range candidates {
		a.releaseAIActivityIfQuiet(managed)
	}
}

// Caller holds a.mu.
func (a *App) aiLatchStillSupportedLocked(managed *managedTerminal) bool {
	if _, reported := a.aiSessionEvidenceForTabLocked(managed); reported {
		// A fresh tool report means reconcileAISessionStatusesOnce owns this latch.
		return true
	}
	if managed.aiModelLatch {
		// Pod liveness cannot hold a tool-raised latch: a process blocked on the human is alive too.
		return false
	}
	heartbeat, ok := a.sessionHeartbeats[selectionKey(managed.selection)]
	return ok && time.Since(heartbeat.observedAt) <= sessionHeartbeatTTL
}

func (a *App) selectionsWithPodSessions() []uimodel.Selection {
	seen := make(map[string]struct{})
	var out []uimodel.Selection
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

func (a *App) applySessionHeartbeat(selection uimodel.Selection, activity uiRuntimeActivity) {
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

// A session the observation does not mention is not finished; its socket may not exist yet. Caller holds a.mu.
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

// The heartbeat can only keep a session running, never invent one.
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

func (a *App) forgetSessionHeartbeats(selection uimodel.Selection) {
	key := selectionKey(selection)
	a.mu.Lock()
	delete(a.sessionHeartbeats, key)
	a.mu.Unlock()
}

// An unreadable listing returns nil and the replaced-session check is skipped.
func (a *App) orchestratorAgentConversations(needed bool) map[string]string {
	out := map[string]string{}
	if !needed {
		return out
	}
	agents, err := a.deps.claudeAgents.List()
	if err != nil {
		return out
	}
	for _, agent := range agents {
		out[agent.ID] = agent.SessionID
	}
	return out
}
