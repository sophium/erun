package main

import (
	"time"
)

// session_heartbeat.go now hosts only the 15s ticker and the orchestrator
// turn-boundary reconciliation it drives. It used to also cache a per-env pod
// heartbeat to release the sidebar's AI-busy latch — that latch was a debounced
// function of PTY output volume, and the cache existed only to keep a
// quiet-but-still-running session from being declared finished by silence
// alone. Both are gone: an environment's AI-session status is no longer
// inferred from output at all. It comes from the tool's own structured report
// (erun-common's AISessionStatus), read through environment_activity.go's
// existing idle-status poll, which needs no pod-heartbeat cache because the
// tool's report already says whether it is still running.

// sessionHeartbeatInterval paces the orchestrator-activity/pacing reconcile
// passes below.
const sessionHeartbeatInterval = 15 * time.Second

func (a *App) runSessionHeartbeatPoller(stop <-chan struct{}) {
	ticker := time.NewTicker(sessionHeartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			// Mirrors are re-enumerated here, not only at boot. An env linked
			// while the app is already running — the usual case, since linking is
			// done from the running app — never got a sync worker, so its mirror
			// stayed silently empty until the next restart, indistinguishable
			// from an env with no files. startWorkspaceSyncForSelection
			// re-validates and dedups a running poller, so this settles to a
			// no-op once every linked env is syncing.
			a.reconcileWorkspaceSyncForConfiguredEnvs()
			a.reconcileOrchestratorActivity()
			// Runs after reconcileOrchestratorActivity so session.shellRunning is
			// this tick's fresh report before the pacing decision reads it.
			a.reconcileOrchestratorPacing()
		}
	}
}

// reconcileOrchestratorActivity publishes what each orchestrator said about
// itself. The agent writes a turn-boundary report; this turns it into the
// sidebar's spinner.
//
// It re-emits every tick, busy or not, rather than only when the state
// changes. The spinner used to be lit exclusively by the one
// false→true transition, and orchestratorInfo — the snapshot the frontend
// boots and re-renders from — carried no busy flag at all, so anything that
// lost that single event (a remount after the transition, a window reopen, a
// listener that attached a beat late, one dropped event on a days-old
// process) left the row reading "idle" for however long the rest of the turn
// ran, which for a long turn is tens of minutes or more. At a 15s tick over a
// handful of orchestrators the extra events are inexpensive, and they make a
// dropped or mistimed one self-heal within one tick instead of staying wrong
// until the busy state itself next changes. session.aiBusy is still recorded
// on every pass — that is the other half of the fix: it is what
// orchestratorInfoFor now reads into orchestratorInfo.Busy, so a snapshot
// (ListOrchestrators, runningOrchestratorInfo) carries the true state
// directly instead of depending solely on this event ever reaching the
// frontend's listener.
func (a *App) reconcileOrchestratorActivity() {
	now := time.Now()
	a.mu.Lock()
	type row struct {
		id             string
		serial         int
		alive          bool
		launchID       string
		conversationID string
	}
	rows := make([]row, 0, len(a.orchestrators))
	for id, session := range a.orchestrators {
		if session == nil || session.transient {
			continue
		}
		// Whether the desktop can still see the session that writes the report
		// decides which staleness bound it ages out on. A live session's turn is
		// allowed to be long; a session we can no longer see must not keep its
		// "working" past the short bound, because nothing will ever clear it.
		managed := a.sessions[orchestratorSessionKey(id)]
		rows = append(rows, row{
			id:             id,
			serial:         session.serial,
			alive:          managed != nil && !managed.closed,
			launchID:       session.launchID,
			conversationID: session.conversationID,
		})
	}
	a.mu.Unlock()

	for _, r := range rows {
		activity, ok := readOrchestratorActivity(r.id, now, r.alive)
		busy := ok && activity.Busy
		// A background shell is a fact independent of the turn's own busy/idle
		// state — it can keep running after the turn that started it ends — so
		// it gets the same re-emit-every-tick treatment on its own, not folded
		// into the busy report above.
		//
		// The report has to name the session that wrote it and be checked against
		// this orchestrator's own session: sessionAlive alone is computed per
		// orchestrator id, not per session, so a report a replaced session left
		// behind would otherwise borrow its successor's liveness. The comparison
		// is against what the CURRENT launch's session reports being on, not the
		// derived id — a session that moved to a conversation of its own is still
		// this orchestrator's session, and comparing against the derivation would
		// reject every report it writes.
		liveConversationID := orchestratorLiveConversationForLaunch(r.id, r.launchID, r.conversationID)
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
		a.emitAIActivity(r.serial, busy)
		a.emitOrchestratorShellActivity(r.serial, shellRunning, shell.Command, shell.AtUnix)
	}
}

// emitOrchestratorShellActivity publishes what an orchestrator's background
// shell report says. startedAtUnix is only meaningful while running is true;
// the caller (reconcileOrchestratorActivity) always passes the report's own
// AtUnix, which is the report's write time either way and harmless to send
// when not running since the frontend only reads it alongside Running.
func (a *App) emitOrchestratorShellActivity(sessionID int, running bool, command string, startedAtUnix int64) {
	a.emitEvent(orchestratorShellEvent, orchestratorShellActivityPayload{
		SessionID:     sessionID,
		Running:       running,
		Command:       command,
		StartedAtUnix: startedAtUnix,
	})
}
