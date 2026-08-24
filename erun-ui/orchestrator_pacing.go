package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// A turn boundary is not the only place an orchestrator needs the pacing
// contract in front of it. The skill text (erun-orchestrate/SKILL.md) already
// asks it to come back roughly every five minutes and never exit its loop, but
// that is guidance the model has to keep remembering across however much
// context sits between it and the reasoning that set it — a compaction, a long
// tool call, a connection error the harness swallowed. Nothing re-states it
// between boundaries, so a session that stalls mid-turn (a dropped connection
// it never retried, a turn that quietly ended early) can sit idle until the
// operator happens to notice.
//
// This file re-states the contract into a session whose own activity report
// (orchestrator_activity.go) has gone quiet, on the same 15s tick that already
// polls everything else session-shaped. It reuses that report rather than a
// report of its own: a turn boundary that renews it is exactly the evidence
// that the session is not stalled, whether the last write said busy or idle.

// orchestratorPacingStaleAfter is how long a report may go unrenewed before the
// session behind it reads as quiet. A working orchestrator renews the report on
// every turn boundary and every tool call, so this only fires on a session that
// has genuinely stopped moving.
const orchestratorPacingStaleAfter = 10 * time.Minute

// orchestratorPacingMaxNudges bounds how many consecutive un-answered nudges one
// orchestrator gets before erun stops and says so — one hour of nudging a
// session that never answers is not recovery, it is erun talking to itself.
const orchestratorPacingMaxNudges = 6

// orchestratorPacingNudgeSettle spaces the text write and the carriage return
// that submits it, mirroring the settle nudgeAIRepaint already uses so the two
// writes are never coalesced into one read on the pty's far side. A package
// variable so tests can drop it to zero.
var orchestratorPacingNudgeSettle = 150 * time.Millisecond

// orchestratorPacingNudgeText restates the pacing contract verbatim, plus the
// one clause that makes it a no-op for a session that is genuinely finished:
// erun cannot know completion, but the session can, and asking is cheaper than
// guessing or nudging forever.
const orchestratorPacingNudgeText = "Keep pacing yourself, on connection errors wait and resume, do not exit this loop. " +
	"If the assigned task is already complete and verified, say so in one line and stop."

// orchestratorPacingActivity is the parsed report, independent of the TTL
// readOrchestratorActivity applies for the busy spinner — that bound exists for
// a different question ("is this row still spinning") and is left untouched.
type orchestratorPacingActivity struct {
	activity orchestratorActivity
	at       time.Time
}

// readOrchestratorPacingActivity reads the same per-orchestrator report
// orchestrator_activity.go writes, raw: no staleness bound applied here, so the
// caller can measure staleness against the ten-minute pacing bound instead of
// the spinner's own two- or thirty-minute one.
func readOrchestratorPacingActivity(id string) (orchestratorPacingActivity, bool) {
	path := orchestratorActivityPath(id)
	if path == "" {
		return orchestratorPacingActivity{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorPacingActivity{}, false
	}
	var activity orchestratorActivity
	if err := json.Unmarshal(data, &activity); err != nil || activity.AtUnix <= 0 {
		return orchestratorPacingActivity{}, false
	}
	return orchestratorPacingActivity{activity: activity, at: time.Unix(activity.AtUnix, 0)}, true
}

// orchestratorPacingCandidate is one orchestrator as the reconciler decides for
// it — gathered so the decision itself is a pure function, testable without
// touching a file or a lock.
type orchestratorPacingCandidate struct {
	alive        bool
	shellRunning bool
	lastActiveAt time.Time
	nudgeCount   int
	capped       bool
}

type orchestratorPacingDecision int

const (
	orchestratorPacingNone orchestratorPacingDecision = iota
	orchestratorPacingNudge
	orchestratorPacingCap
)

// decideOrchestratorPacing is the whole bound: a session the desktop cannot
// see, one running a background shell, or one already past the cap gets no
// nudge (a background shell is a fact independent of the turn's own busy/idle
// state, per orchestrator_shell_activity.go — it can legitimately keep running
// after the turn that started it ends, and nudging into it would interrupt
// something the orchestrator deliberately left going). A candidate that is
// stale and not yet capped gets nudged; one that just crossed the cap gets the
// one-time notice instead.
func decideOrchestratorPacing(c orchestratorPacingCandidate, now time.Time) orchestratorPacingDecision {
	if !c.alive || c.shellRunning {
		return orchestratorPacingNone
	}
	if now.Sub(c.lastActiveAt) < orchestratorPacingStaleAfter {
		return orchestratorPacingNone
	}
	if c.capped {
		return orchestratorPacingNone
	}
	if c.nudgeCount >= orchestratorPacingMaxNudges {
		return orchestratorPacingCap
	}
	return orchestratorPacingNudge
}

// orchestratorPacingRow is what the reconciler gathers under a.mu for one
// orchestrator, before making any decision or doing any file/pty IO outside
// the lock.
type orchestratorPacingRow struct {
	id              string
	serial          int
	name            string
	alive           bool
	shellRunning    bool
	startedAt       time.Time
	nudgeCount      int
	capped          bool
	lastNudgeAtUnix int64
}

// reconcileOrchestratorPacing runs on the same 15s tick that already polls
// session heartbeats and orchestrator activity. It is cheap to run every tick:
// the read is a small file per orchestrator, and the decision only ever does
// pty IO for a session that has been quiet for the full ten minutes.
func (a *App) reconcileOrchestratorPacing() {
	now := time.Now()
	a.mu.Lock()
	rows := make([]orchestratorPacingRow, 0, len(a.orchestrators))
	for id, session := range a.orchestrators {
		if session == nil || session.transient {
			continue
		}
		managed := a.sessions[orchestratorSessionKey(id)]
		rows = append(rows, orchestratorPacingRow{
			id:              id,
			serial:          session.serial,
			name:            session.name,
			alive:           managed != nil && !managed.closed,
			shellRunning:    session.shellRunning,
			startedAt:       session.startedAt,
			nudgeCount:      session.pacingNudgeCount,
			capped:          session.pacingCapped,
			lastNudgeAtUnix: session.pacingLastNudgeAtUnix,
		})
	}
	a.mu.Unlock()

	for _, row := range rows {
		a.reconcileOrchestratorPacingOne(row, now)
	}
}

// reconcileOrchestratorPacingOne reads this orchestrator's report, rearms it on
// a fresh busy write, decides, and acts. Split out of reconcileOrchestratorPacing
// to keep both under this module's complexity budget.
func (a *App) reconcileOrchestratorPacingOne(row orchestratorPacingRow, now time.Time) {
	lastActiveAt := row.startedAt
	report, ok := readOrchestratorPacingActivity(row.id)
	if ok && report.at.After(lastActiveAt) {
		lastActiveAt = report.at
	}
	if ok && report.activity.Busy && report.at.Unix() > row.lastNudgeAtUnix {
		a.rearmOrchestratorPacing(row.id)
		row.nudgeCount = 0
		row.capped = false
	}

	candidate := orchestratorPacingCandidate{
		alive:        row.alive,
		shellRunning: row.shellRunning,
		lastActiveAt: lastActiveAt,
		nudgeCount:   row.nudgeCount,
		capped:       row.capped,
	}
	switch decideOrchestratorPacing(candidate, now) {
	case orchestratorPacingNudge:
		a.sendOrchestratorPacingNudge(row.id, row.serial, now)
	case orchestratorPacingCap:
		a.capOrchestratorPacing(row.id, row.serial, row.name)
	}
}

// rearmOrchestratorPacing clears the nudge count and the cap, so the next
// staleness period starts counting from zero. Called both for a fresh busy
// report (this reconciler) and for real operator input into the pane
// (SendSessionInput) — the two rearm paths the cap bound names.
func (a *App) rearmOrchestratorPacing(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if session := a.orchestrators[id]; session != nil {
		session.pacingNudgeCount = 0
		session.pacingCapped = false
	}
}

// sendOrchestratorPacingNudge records the attempt before writing, so a nudge
// that fails to write still counts against the cap rather than looping forever
// against a pty that cannot accept input.
func (a *App) sendOrchestratorPacingNudge(id string, serial int, now time.Time) {
	a.mu.Lock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		a.mu.Unlock()
		return
	}
	session.pacingNudgeCount++
	session.pacingLastNudgeAtUnix = now.Unix()
	count := session.pacingNudgeCount
	a.mu.Unlock()

	if !a.writeOrchestratorPacingNudge(managed) {
		return
	}
	a.emitOrchestratorPacingMarker(serial, count)
}

// writeOrchestratorPacingNudge writes the pacing text, then — after a short
// settle — a bare carriage return to submit it, the same two-write shape
// SendSessionInput's callers already rely on for the harness's own prompt
// submission. A single combined write risks the harness reading the CR as part
// of the pasted text rather than as Enter.
func (a *App) writeOrchestratorPacingNudge(managed *managedTerminal) bool {
	session, ok := a.liveSessionOf(managed)
	if !ok {
		return false
	}
	if _, err := io.WriteString(session, orchestratorPacingNudgeText); err != nil {
		return false
	}
	time.Sleep(orchestratorPacingNudgeSettle)
	session, ok = a.liveSessionOf(managed)
	if !ok {
		return false
	}
	_, err := io.WriteString(session, "\r")
	return err == nil
}

// liveSessionOf reads the managed terminal's current pty under lock, so the
// nudge writer never touches one a concurrent close already tore down.
func (a *App) liveSessionOf(managed *managedTerminal) (terminalSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed.closed || managed.session == nil {
		return nil, false
	}
	return managed.session, true
}

// capOrchestratorPacing latches the cap so the reconciler stops nudging and
// reports why. Guarded on session.pacingCapped so a repeat tick (the
// reconciler runs every 15s; the cap condition holds until rearmed) posts the
// notice once rather than every tick until the operator answers.
func (a *App) capOrchestratorPacing(id string, serial int, name string) {
	a.mu.Lock()
	session := a.orchestrators[id]
	if session == nil || session.pacingCapped {
		a.mu.Unlock()
		return
	}
	session.pacingCapped = true
	a.mu.Unlock()
	a.emitOrchestratorPacingCappedMarker(serial)
	a.emitAppNotification("warning", orchestratorPacingCappedNotice(name))
}

// emitOrchestratorPacingMarker names the nudge and its count in the pane, in
// the same style emitReconnectMarker already uses for terminal status lines —
// typing into the operator's session without saying so is exactly the
// state-without-affordance gap this exists to avoid.
func (a *App) emitOrchestratorPacingMarker(sessionID, count int) {
	marker := fmt.Sprintf("\r\n\x1b[2;33m── pacing nudge %d/%d sent — no activity report for %s ──\x1b[0m\r\n",
		count, orchestratorPacingMaxNudges, orchestratorPacingStaleAfter)
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// emitOrchestratorPacingCappedMarker names the recovery the same way the other
// terminal markers do: reply in the pane, or restart it.
func (a *App) emitOrchestratorPacingCappedMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── stopped pacing nudges after repeated silence — reply in this pane or restart the orchestrator to resume ──\x1b[0m\r\n"
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// orchestratorPacingCappedNotice is the notification the titlebar/notification
// surface renders when the cap fires — the marker above lives in a terminal
// pane the operator may not currently be looking at.
func orchestratorPacingCappedNotice(name string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "An orchestrator"
	}
	return fmt.Sprintf("%s stopped answering pacing nudges after %d attempts over about an hour. "+
		"Reply in its pane or restart it to resume.", label, orchestratorPacingMaxNudges)
}
