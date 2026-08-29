package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
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

// orchestratorPacingStaleAfter and orchestratorPacingMaxNudges are the
// unconfigured defaults, kept as their own named values (rather than reading
// eruncommon's constants inline) because orchestrator_pacing_test.go stages
// candidate timestamps and counts directly off them. They are defined equal to
// eruncommon.DefaultWhipStaleAfter/DefaultWhipMaxNudges, so an unconfigured
// install's actual runtime bound (orchestratorWhipConfig, below) always agrees
// with what these tests stage against.
const (
	orchestratorPacingStaleAfter = eruncommon.DefaultWhipStaleAfter
	orchestratorPacingMaxNudges  = eruncommon.DefaultWhipMaxNudges
)

// orchestratorPacingNudgeSettle spaces the text write and the carriage return
// that submits it, mirroring the settle nudgeAIRepaint already uses so the two
// writes are never coalesced into one read on the pty's far side. A package
// variable so tests can drop it to zero.
var orchestratorPacingNudgeSettle = 150 * time.Millisecond

// orchestratorPacingNudgeText is the unconfigured default nudge text. The
// message is now editable via ~/.erun/config.yaml's `whip.message`; this
// constant is what an install that configures nothing keeps getting, verbatim.
const orchestratorPacingNudgeText = eruncommon.DefaultWhipMessage

// orchestratorWhipConfig is the pacing pass's resolved, live-reloadable
// configuration (message/stale-threshold/cap), read from the operator's global
// config once per reconciler tick (refreshOrchestratorWhipConfig) so an edit to
// ~/.erun/config.yaml takes effect on the next tick without a rebuild or
// restart. Guarded by a mutex because the manual whip-now entrypoints
// (whipOrchestratorNow/whipAllOrchestratorsNow) can read it from a different
// goroutine than the reconciler tick.
var (
	orchestratorWhipConfigMu sync.RWMutex
	orchestratorWhipConfig   = eruncommon.ResolveWhipConfig(nil)
)

func setOrchestratorWhipConfig(cfg eruncommon.WhipConfig) {
	orchestratorWhipConfigMu.Lock()
	orchestratorWhipConfig = cfg
	orchestratorWhipConfigMu.Unlock()
}

func getOrchestratorWhipConfig() eruncommon.WhipConfig {
	orchestratorWhipConfigMu.RLock()
	defer orchestratorWhipConfigMu.RUnlock()
	return orchestratorWhipConfig
}

// refreshOrchestratorWhipConfig re-reads ~/.erun/config.yaml's whip override
// and resolves it against today's defaults. Best-effort: a missing or
// unreadable root config resolves to the zero ERunConfig, whose nil Whip
// override keeps orchestratorWhipConfig on exactly today's behaviour — the same
// "unconfigured install is unaffected" contract erun-common's ResolveWhipConfig
// makes for every transport.
func refreshOrchestratorWhipConfig() {
	config, _, _ := eruncommon.LoadERunConfig()
	setOrchestratorWhipConfig(eruncommon.ResolveWhipConfig(config.Whip))
}

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
//
// This deliberately carries no background-shell fact. It used to gate the
// nudge on one (orchestrator-shell-activity), reasoning that a shell left
// running was evidence the orchestrator meant to be quiet — but a background
// shell is a fact about a *shell*, not about the turn behind it, and a
// long-running build is the single most likely thing to be in flight when a
// turn dies mid-response. Gating on it suppressed the nudge exactly when it
// was needed most (erun#1376). The shell-activity report stays exactly as it
// is for the shell indicator; pacing just stops reading it.
type orchestratorPacingCandidate struct {
	alive        bool
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

// orchestratorPacingReason names why decideOrchestratorPacing returned what it
// did, independent of the decision itself: two candidates can both resolve to
// orchestratorPacingNone for different reasons, and the reconciler logs the
// reason (not just the decision) so a quiet pane and a suppressed one are
// distinguishable from the log rather than indistinguishable from silence.
type orchestratorPacingReason string

const (
	orchestratorPacingReasonNotAlive      orchestratorPacingReason = "not-alive"
	orchestratorPacingReasonFresh         orchestratorPacingReason = "fresh"
	orchestratorPacingReasonAlreadyCapped orchestratorPacingReason = "already-capped"
	orchestratorPacingReasonCapCrossed    orchestratorPacingReason = "cap-crossed"
	orchestratorPacingReasonNudge         orchestratorPacingReason = "nudge"
)

// decideOrchestratorPacing is the automatic-pass bound (explicit=false): a
// session the desktop cannot see, or one already past the cap, gets no nudge.
// A candidate that is stale and not yet capped gets nudged; one that just
// crossed the cap gets the one-time notice instead. It delegates to
// eruncommon.DecideWhip (the population-agnostic core, shared with the
// environment-agent pusher and the CLI/MCP transports) against the live
// orchestratorWhipConfig, so a configured message/threshold/cap changes this
// decision without a rebuild while every existing caller and test here keeps
// its original two-argument shape.
func decideOrchestratorPacing(c orchestratorPacingCandidate, now time.Time) (orchestratorPacingDecision, orchestratorPacingReason) {
	return decideOrchestratorWhip(c, now, false)
}

// decideOrchestratorWhip is decideOrchestratorPacing's explicit-aware form: a
// manual, operator-triggered whip (explicit=true) ignores staleness — the
// operator clicking/invoking it now is the assertion that this session should
// be pushed regardless of how recently it moved — but never bypasses the cap
// or an already-capped session, exactly as DecideWhip's explicit contract
// requires.
func decideOrchestratorWhip(c orchestratorPacingCandidate, now time.Time, explicit bool) (orchestratorPacingDecision, orchestratorPacingReason) {
	candidate := eruncommon.WhipCandidate{
		Kind:         eruncommon.WhipTargetOrchestrator,
		Reachable:    true, // the desktop holds this orchestrator's PTY itself
		Alive:        c.alive,
		LastActiveAt: c.lastActiveAt,
		NudgeCount:   c.nudgeCount,
		Capped:       c.capped,
	}
	decision, reason := eruncommon.DecideWhip(candidate, now, getOrchestratorWhipConfig(), explicit)
	return orchestratorPacingDecisionFromWhip(decision), orchestratorPacingReasonFromWhip(reason)
}

func orchestratorPacingDecisionFromWhip(decision eruncommon.WhipDecision) orchestratorPacingDecision {
	switch decision {
	case eruncommon.WhipDecisionNudge:
		return orchestratorPacingNudge
	case eruncommon.WhipDecisionCap:
		return orchestratorPacingCap
	default:
		return orchestratorPacingNone
	}
}

// orchestratorPacingReasonFromWhip translates every reason DecideWhip can
// return for a Reachable candidate. WhipReasonUnreachable never appears here:
// an orchestrator's own reconciler always sets Reachable true (it holds the
// PTY), unlike the CLI/MCP transports, which never can.
func orchestratorPacingReasonFromWhip(reason eruncommon.WhipReason) orchestratorPacingReason {
	switch reason {
	case eruncommon.WhipReasonNotAlive:
		return orchestratorPacingReasonNotAlive
	case eruncommon.WhipReasonAlreadyCapped:
		return orchestratorPacingReasonAlreadyCapped
	case eruncommon.WhipReasonCapCrossed:
		return orchestratorPacingReasonCapCrossed
	case eruncommon.WhipReasonNudge:
		return orchestratorPacingReasonNudge
	default:
		return orchestratorPacingReasonFresh
	}
}

// whipDecisionFromOrchestratorPacing is orchestratorPacingDecisionFromWhip's
// inverse, used by WhipNow (whip.go) to fold an orchestrator outcome into the
// same eruncommon.WhipResult shape the environment side reports in.
func whipDecisionFromOrchestratorPacing(decision orchestratorPacingDecision) eruncommon.WhipDecision {
	switch decision {
	case orchestratorPacingNudge:
		return eruncommon.WhipDecisionNudge
	case orchestratorPacingCap:
		return eruncommon.WhipDecisionCap
	default:
		return eruncommon.WhipDecisionNone
	}
}

// whipReasonFromOrchestratorPacing is orchestratorPacingReasonFromWhip's
// inverse; see whipDecisionFromOrchestratorPacing.
func whipReasonFromOrchestratorPacing(reason orchestratorPacingReason) eruncommon.WhipReason {
	switch reason {
	case orchestratorPacingReasonNotAlive:
		return eruncommon.WhipReasonNotAlive
	case orchestratorPacingReasonAlreadyCapped:
		return eruncommon.WhipReasonAlreadyCapped
	case orchestratorPacingReasonCapCrossed:
		return eruncommon.WhipReasonCapCrossed
	case orchestratorPacingReasonNudge:
		return eruncommon.WhipReasonNudge
	default:
		return eruncommon.WhipReasonFresh
	}
}

// orchestratorPacingRow is what the reconciler gathers under a.mu for one
// orchestrator, before making any decision or doing any file/pty IO outside
// the lock.
type orchestratorPacingRow struct {
	id               string
	serial           int
	name             string
	alive            bool
	startedAt        time.Time
	nudgeCount       int
	capped           bool
	lastNudgeAtUnix  int64
	lastLoggedReason orchestratorPacingReason
}

// reconcileOrchestratorPacing runs on the same 15s tick that already polls
// session heartbeats and orchestrator activity. It is cheap to run every tick:
// the read is a small file per orchestrator, and the decision only ever does
// pty IO for a session that has been quiet for the full ten minutes.
func (a *App) reconcileOrchestratorPacing() {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	for _, row := range a.orchestratorPacingRows() {
		a.reconcileOrchestratorPacingOne(row, now, false)
	}
}

// orchestratorPacingRows gathers this tick's candidates under a.mu, before any
// decision or file/pty IO outside the lock: every non-transient orchestrator.
func (a *App) orchestratorPacingRows() []orchestratorPacingRow {
	a.mu.Lock()
	defer a.mu.Unlock()
	rows := make([]orchestratorPacingRow, 0, len(a.orchestrators))
	for orchestratorID, session := range a.orchestrators {
		if session == nil || session.transient {
			continue
		}
		managed := a.sessions[orchestratorSessionKey(orchestratorID)]
		rows = append(rows, orchestratorPacingRow{
			id:               orchestratorID,
			serial:           session.serial,
			name:             session.name,
			alive:            managed != nil && !managed.closed,
			startedAt:        session.startedAt,
			nudgeCount:       session.pacingNudgeCount,
			capped:           session.pacingCapped,
			lastNudgeAtUnix:  session.pacingLastNudgeAtUnix,
			lastLoggedReason: session.pacingLastReason,
		})
	}
	return rows
}

// orchestratorWhipOutcome is one orchestrator's result from an explicit
// whip-everything pass — the visible record an operator judges the feature
// by: which orchestrator, what was decided, and why.
type orchestratorWhipOutcome struct {
	id       string
	name     string
	decision orchestratorPacingDecision
	reason   orchestratorPacingReason
}

// whipAllOrchestratorsNow is the section-level explicit whip: every live,
// non-transient orchestrator, pushed now regardless of staleness, each still
// bound by its own cap. Named per orchestrator in the return so a caller can
// report exactly who was pushed and who was skipped, and why skipped.
func (a *App) whipAllOrchestratorsNow() []orchestratorWhipOutcome {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	rows := a.orchestratorPacingRows()
	outcomes := make([]orchestratorWhipOutcome, 0, len(rows))
	for _, row := range rows {
		decision, reason := a.reconcileOrchestratorPacingOne(row, now, true)
		outcomes = append(outcomes, orchestratorWhipOutcome{id: row.id, name: row.name, decision: decision, reason: reason})
	}
	return outcomes
}

// orchestratorPacingActivitySignal is what the reconciler could tell about the
// orchestrator's last report, carried through to the marker so a session that
// died mid-turn reads differently from one that simply went quiet after
// finishing (erun#1376): a report has to exist and say "busy" for the marker
// to call it a died turn, since idle-then-quiet is the ordinary, unremarkable
// case the pacing contract expects.
type orchestratorPacingActivitySignal int

const (
	orchestratorPacingSignalIdle orchestratorPacingActivitySignal = iota
	orchestratorPacingSignalNoReport
	orchestratorPacingSignalDied
)

// orchestratorPacingSignalFor classifies the last report the reconciler read,
// independent of whether it was stale enough to nudge on: hasReport is false
// only when no report has ever been read for this orchestrator (readOrchestratorPacingActivity's
// ok), and lastBusy is that report's own "busy" field.
func orchestratorPacingSignalFor(hasReport, lastBusy bool) orchestratorPacingActivitySignal {
	if !hasReport {
		return orchestratorPacingSignalNoReport
	}
	if lastBusy {
		return orchestratorPacingSignalDied
	}
	return orchestratorPacingSignalIdle
}

// reconcileOrchestratorPacingOne reads this orchestrator's report, rearms it on
// a fresh busy write, decides, and acts. Split out of reconcileOrchestratorPacing
// to keep both under this module's complexity budget.
func (a *App) reconcileOrchestratorPacingOne(row orchestratorPacingRow, now time.Time, explicit bool) (orchestratorPacingDecision, orchestratorPacingReason) {
	lastActiveAt := row.startedAt
	report, ok := readOrchestratorPacingActivity(row.id)
	if ok && report.at.After(lastActiveAt) {
		lastActiveAt = report.at
	}
	// A report written after the last nudge is the turn boundary itself,
	// whether that turn ended busy or idle: the session did something in
	// response to being asked. Requiring Busy here missed exactly the
	// compliant case — a short reply that returns to idle before the next
	// tick samples it — so a session answering every nudge still climbed
	// toward the cap.
	if ok && report.at.Unix() > row.lastNudgeAtUnix {
		a.rearmOrchestratorPacing(row.id)
		row.nudgeCount = 0
		row.capped = false
	}

	candidate := orchestratorPacingCandidate{
		alive:        row.alive,
		lastActiveAt: lastActiveAt,
		nudgeCount:   row.nudgeCount,
		capped:       row.capped,
	}
	elapsed := now.Sub(lastActiveAt)
	decision, reason := decideOrchestratorWhip(candidate, now, explicit)
	a.logOrchestratorPacingTransition(row, reason, elapsed)

	switch decision {
	case orchestratorPacingNudge:
		signal := orchestratorPacingSignalFor(ok, ok && report.activity.Busy)
		a.sendOrchestratorPacingNudge(row.id, row.serial, now, elapsed, signal)
	case orchestratorPacingCap:
		a.capOrchestratorPacing(row.id, row.serial, row.name)
	}
	return decision, reason
}

// logOrchestratorPacingTransition is the fix for the silent-suppression half of
// erun#1376: every decision decideOrchestratorPacing can reach — not just
// "nudge" — gets a durable, one-line record naming the orchestrator, the
// measured quiet period, and which reason applied, so a quiet pane and a
// suppressed one are told apart from the log rather than being indistinguishable.
// It logs only on a transition (this orchestrator's reason changed since the
// last tick), not on every 15s tick, since most orchestrators spend most of
// their life in "fresh" and a per-tick line would drown the signal.
func (a *App) logOrchestratorPacingTransition(row orchestratorPacingRow, reason orchestratorPacingReason, elapsed time.Duration) {
	if reason == row.lastLoggedReason {
		return
	}
	a.mu.Lock()
	if session := a.orchestrators[row.id]; session != nil {
		session.pacingLastReason = reason
	}
	a.mu.Unlock()
	label := strings.TrimSpace(row.name)
	if label == "" {
		label = row.id
	}
	log.Printf("erun-app: orchestrator %s pacing decision=%s quiet=%s", label, reason, elapsed.Round(time.Second))
}

// rearmOrchestratorPacing clears the nudge count and the cap, so the next
// staleness period starts counting from zero. Called both for a fresh
// activity report written after the last nudge (this reconciler) and for
// real operator input into the pane (SendSessionInput) — the two rearm
// paths the cap bound names.
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
func (a *App) sendOrchestratorPacingNudge(id string, serial int, now time.Time, elapsed time.Duration, signal orchestratorPacingActivitySignal) {
	a.mu.Lock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		a.mu.Unlock()
		return
	}
	if typedRecentlyLocked(managed) {
		// The operator is mid-sentence in this pane. Writing the nudge text
		// plus its submitting "\r" now would glue onto whatever they are
		// typing, the same hazard the AI repaint nudge stands down for
		// (#1330). Skip without touching the nudge count or timestamp so
		// this does not cost against orchestratorPacingMaxNudges; the
		// reconciler retries on its next 15s tick once they pause.
		a.mu.Unlock()
		log.Printf("erun-app: orchestrator %s pacing nudge deferred: pane is being typed into", id)
		return
	}
	session.pacingNudgeCount++
	session.pacingLastNudgeAtUnix = now.Unix()
	count := session.pacingNudgeCount
	a.mu.Unlock()

	if !a.writeOrchestratorPacingNudge(managed) {
		log.Printf("erun-app: orchestrator %s pacing nudge write failed", id)
		return
	}
	a.emitOrchestratorPacingMarker(serial, count, elapsed, signal)
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
	if _, err := io.WriteString(session, getOrchestratorWhipConfig().Message); err != nil {
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
//
// elapsed is the reconciler's own measured now.Sub(lastActiveAt), never the
// orchestratorPacingStaleAfter constant: a session quiet for 20 minutes must
// read as 20 minutes, not as the ten-minute contract restated back at the
// operator as if it had been honoured (erun#1376).
func (a *App) emitOrchestratorPacingMarker(sessionID, count int, elapsed time.Duration, signal orchestratorPacingActivitySignal) {
	marker := fmt.Sprintf("\r\n\x1b[2;33m── pacing nudge %d/%d sent — %s ──\x1b[0m\r\n",
		count, getOrchestratorWhipConfig().MaxNudges, orchestratorPacingQuietDescription(elapsed, signal))
	a.emitEvent(terminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// orchestratorPacingQuietDescription renders the measured quiet period, plus —
// when the last report this orchestrator wrote said "busy" and was never
// followed by an idle one — a distinct note that the turn behind it may have
// died rather than simply gone quiet: a report stuck on "busy" for a full
// staleness period is the harness never reaching its own turn boundary (a
// dropped connection, a crash), not an orchestrator that finished and stopped
// reporting. Without this, both look identical in the pane and the operator
// has to go read a transcript to tell them apart (erun#1376).
func orchestratorPacingQuietDescription(elapsed time.Duration, signal orchestratorPacingActivitySignal) string {
	quiet := elapsed.Round(time.Second)
	switch signal {
	case orchestratorPacingSignalDied:
		return fmt.Sprintf("no activity report for %s — last report said mid-turn, so the turn may have died without one", quiet)
	case orchestratorPacingSignalNoReport:
		return fmt.Sprintf("no activity report received in the %s since it started", quiet)
	default:
		return fmt.Sprintf("no activity report for %s", quiet)
	}
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
		"Reply in its pane or restart it to resume.", label, getOrchestratorWhipConfig().MaxNudges)
}
