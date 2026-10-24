package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// The skill text asks an orchestrator to keep pacing itself, but nothing
// re-states that between turn boundaries, so a session that stalls mid-turn can
// sit idle until the operator notices. This file re-states the contract into a
// session whose activity report (orchestrator_activity.go) has gone quiet, on
// the shared 15s tick. A turn boundary that renews that report is the evidence
// the session is not stalled, whether the last write said busy or idle.

// orchestratorPacingStaleAfter and orchestratorPacingMaxNudges are the
// unconfigured defaults, named here because tests stage candidates off them;
// they equal orchestrator.DefaultWhipStaleAfter/DefaultWhipMaxNudges so an
// unconfigured install's runtime bound agrees with what tests stage against.
const (
	orchestratorPacingStaleAfter = orchestrator.DefaultWhipStaleAfter
	orchestratorPacingMaxNudges  = orchestrator.DefaultWhipMaxNudges
)

// orchestratorPacingNudgeSettle spaces the text write and the carriage return
// that submits it, mirroring the settle nudgeAIRepaint already uses so the two
// writes are never coalesced into one read on the pty's far side. A package
// variable so tests can drop it to zero.
var orchestratorPacingNudgeSettle = 150 * time.Millisecond

// orchestratorPacingNudgeText is the unconfigured default nudge text;
// ~/.erun/config.yaml's `whip.message` overrides it.
const orchestratorPacingNudgeText = orchestrator.DefaultWhipMessage

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
// This deliberately carries no background-shell fact: a running shell says
// nothing about the turn behind it, and a long build is the most likely thing
// in flight when a turn dies mid-response, so gating on it would suppress the
// nudge exactly when it is needed most.
type orchestratorPacingCandidate struct {
	alive        bool
	lastActiveAt time.Time
	nudgeCount   int
	capped       bool
	// unmanaged marks a candidate this desktop holds no session for (started in
	// a terminal or by a previous desktop instance). It is a transport fact, not
	// a claim the session is dead; it may well be alive.
	unmanaged bool
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
	// orchestratorPacingReasonUnreachable is a configured orchestrator this
	// desktop holds no session for. It is deliberately not NotAlive: such
	// sessions are usually alive, just unreachable from here.
	orchestratorPacingReasonUnreachable orchestratorPacingReason = "unreachable-from-transport"
)

// decideOrchestratorPacing is the automatic-pass bound (explicit=false): a
// session the desktop cannot see, or one already past the cap, gets no nudge.
// A candidate that is stale and not yet capped gets nudged; one that just
// crossed the cap gets the one-time notice instead. It delegates to
// orchestrator.DecideWhip against the live orchestratorWhipConfig, so a
// configured threshold/cap applies without a rebuild.
func decideOrchestratorPacing(c orchestratorPacingCandidate, now time.Time) (orchestratorPacingDecision, orchestratorPacingReason) {
	return decideOrchestratorWhip(c, now, false)
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
	// envs is this orchestrator's configured link scope, so the env-aware gate
	// only consults environments it actually linked.
	envs []configpkg.OrchestratorEnvConfig
	// unmanaged marks a configured orchestrator this desktop holds no session
	// for: nothing acts on its row, but the reconciler still decides and logs
	// for it (see orchestratorPacingUnmanagedRows).
	unmanaged bool
}

// quietMeasured is false when this row has no reference point to measure a
// quiet period against: an orchestrator the desktop holds no session for has no
// start time to count from, and without an activity report either there is
// nothing else to measure. The log names that rather than printing a duration
// measured from the zero time, which reads as an enormous, invented outage.
func (r orchestratorPacingRow) quietMeasured(hasReport bool) bool {
	return !r.unmanaged || hasReport
}

// reconcileOrchestratorPacing runs on the same 15s tick that already polls
// session heartbeats and orchestrator activity. It is cheap to run every tick:
// the read is a small file per orchestrator plus one config read, and the
// decision only ever does pty IO for a session that has been quiet for the full
// ten minutes.
func (a *App) reconcileOrchestratorPacing() {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	rows := a.orchestratorPacingRows()
	managed := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		managed[row.id] = struct{}{}
		a.reconcileOrchestratorPacingOne(row, now, false)
	}
	a.forgetPacedOrchestrators(managed)
	// The configured orchestrators the pass above cannot see, so the decision
	// line below covers the whole configured population rather than only the
	// part of it this desktop happens to hold a PTY for.
	for _, row := range a.orchestratorPacingUnmanagedRows(managed) {
		a.reconcileOrchestratorPacingOne(row, now, false)
	}
}

// orchestratorPacingUnreachable reports whether a configured orchestrator is
// being driven by a session this desktop does not own: it has no session here
// while its own hooks are reporting one recently enough that the pacer would
// have been deciding for it. It is what lets the hover card tell "no nudge was
// needed" apart from "no nudge was possible from this desktop" — the two states
// a nudge count frozen at zero cannot distinguish.
//
// It uses the pacer's own stale bound so it agrees with the reconciler. An
// orchestrator with no session anywhere reports false; the card already shows
// "Stopped".
func orchestratorPacingUnreachable(id string, now time.Time) bool {
	report, ok := readOrchestratorPacingActivity(id)
	return ok && now.Sub(report.at) <= getOrchestratorWhipConfig().StaleAfter
}

// orchestratorPacingUnmanagedRows names every configured orchestrator this
// desktop holds no session for, one row each, carrying the reason last logged
// for it so its line obeys the same once-per-transition rule a session-backed
// row's does.
//
// It keeps the pacing log's coverage equal to the configured population: a
// session started outside this desktop has no session object here, and an
// orchestrator that is never evaluated is less legible than a suppressed one.
func (a *App) orchestratorPacingUnmanagedRows(managed map[string]struct{}) []orchestratorPacingRow {
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	rows := make([]orchestratorPacingRow, 0, len(configs))
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" {
			continue
		}
		if _, ok := managed[id]; ok {
			continue
		}
		if _, ok := a.orchestrators[id]; ok {
			continue
		}
		rows = append(rows, orchestratorPacingRow{
			id:               id,
			name:             config.Name,
			unmanaged:        true,
			lastLoggedReason: a.unmanagedPacingReason[id],
		})
	}
	return rows
}

// forgetPacedOrchestrators drops the remembered line of every orchestrator this
// pass observed a session for, because that orchestrator is no longer in the
// unpaced population: its line now comes from the session's own row. Clearing
// it here is what lets an orchestrator that later comes back to the unpaced
// population — a session stopped, or one that was never the desktop's — report
// its decision again instead of inheriting a line written before a session this
// desktop has since run.
func (a *App) forgetPacedOrchestrators(managed map[string]struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.unmanagedPacingReason {
		if _, ok := managed[id]; ok {
			delete(a.unmanagedPacingReason, id)
		}
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
			envs:             session.envs,
		})
	}
	return rows
}

// orchestratorPacingActivitySignal is what the reconciler could tell about the
// orchestrator's last report, carried through to the marker so a session that
// died mid-turn reads differently from one that simply went quiet after
// finishing: a report has to exist and say "busy" for the marker
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
	// whether that turn ended busy or idle. Requiring Busy would miss a short
	// reply that returns to idle before the next tick samples it.
	if ok && report.at.Unix() > row.lastNudgeAtUnix {
		a.rearmOrchestratorPacing(row.id)
		row.nudgeCount = 0
		row.capped = false
	}

	elapsed := now.Sub(lastActiveAt)
	envBusy := orchestratorLinkedEnvBusyStateFor(row.id, row.envs, a.envActivitySnapshot())
	if a.orchestratorPacingSuppressedByLinkedEnv(row, explicit, envBusy, elapsed) {
		a.logOrchestratorPacingTransition(row, orchestratorPacingReasonEnvBusy, elapsed, true)
		return orchestratorPacingNone, orchestratorPacingReasonEnvBusy
	}

	candidate := orchestratorPacingCandidate{
		alive:        row.alive,
		lastActiveAt: lastActiveAt,
		nudgeCount:   row.nudgeCount,
		capped:       row.capped,
		unmanaged:    row.unmanaged,
	}
	decision, reason := decideOrchestratorWhip(candidate, now, explicit)
	a.logOrchestratorPacingTransition(row, reason, elapsed, row.quietMeasured(ok))

	switch decision {
	case orchestratorPacingNudge:
		signal := orchestratorPacingSignalFor(ok, ok && report.activity.Busy)
		a.sendOrchestratorPacingNudge(row.id, row.serial, now, elapsed, signal, envBusy.stuckDetail(), explicit)
	case orchestratorPacingCap:
		a.capOrchestratorPacing(row.id, row.serial, row.name, now)
	case orchestratorPacingNone:
	}
	return decision, reason
}

// logOrchestratorPacingTransition records every decision, not just "nudge",
// with the orchestrator, measured quiet period, and reason, so a quiet pane and
// a suppressed one are distinguishable from the log. It logs only when the
// reason changes, since a per-tick line would drown the signal.
//
// quietKnown is false when the row has no reference point, so the log says
// "unknown" instead of a duration measured from the zero time.
func (a *App) logOrchestratorPacingTransition(row orchestratorPacingRow, reason orchestratorPacingReason, elapsed time.Duration, quietKnown bool) {
	if reason == row.lastLoggedReason {
		return
	}
	a.mu.Lock()
	if session := a.orchestrators[row.id]; session != nil {
		session.pacingLastReason = reason
	} else if row.unmanaged {
		if a.unmanagedPacingReason == nil {
			a.unmanagedPacingReason = make(map[string]orchestratorPacingReason)
		}
		a.unmanagedPacingReason[row.id] = reason
	}
	a.mu.Unlock()
	// The id leads because it is the stable key every other surface uses; the
	// display name follows only when it differs.
	subject := strings.TrimSpace(row.id)
	if name := strings.TrimSpace(row.name); name != "" && name != subject {
		subject += " (" + name + ")"
	}
	quiet := "unknown"
	if quietKnown {
		quiet = elapsed.Round(time.Second).String()
	}
	log.Printf("erun-app: orchestrator %s pacing decision=%s quiet=%s", subject, reason, quiet)
}

// rearmOrchestratorPacing clears the nudge count and the cap, so the next
// staleness period starts counting from zero. Called both for a fresh
// activity report written after the last nudge (this reconciler) and for
// real operator input into the pane (SendSessionInput) — the two rearm
// paths the cap bound names.
//
// It deliberately leaves pacingAutoNudgeCount/pacingWhipCount alone: those are
// cumulative history for the hover card, not the cap's budget.
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
// against a pty that cannot accept input. envStuckDetail is non-empty only
// when a linked environment's busy lease was still active but the suppression
// bound was crossed anyway.
//
// explicit distinguishes an operator-triggered whip from the automatic pacer:
// it picks which cumulative history counter to bump, while pacingNudgeCount's
// cap bookkeeping is shared by both.
func (a *App) sendOrchestratorPacingNudge(id string, serial int, now time.Time, elapsed time.Duration, signal orchestratorPacingActivitySignal, envStuckDetail string, explicit bool) {
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
		// typing, the same hazard the AI repaint nudge stands down for.
		// Skip without touching the nudge count or timestamp so
		// this does not cost against orchestratorPacingMaxNudges; the
		// reconciler retries on its next 15s tick once they pause.
		a.mu.Unlock()
		log.Printf("erun-app: orchestrator %s pacing nudge deferred: pane is being typed into", id)
		return
	}
	session.pacingNudgeCount++
	session.pacingLastNudgeAtUnix = now.Unix()
	if explicit {
		session.pacingWhipCount++
		session.pacingLastWhipAtUnix = now.Unix()
	} else {
		session.pacingAutoNudgeCount++
		session.pacingLastAutoNudgeAtUnix = now.Unix()
	}
	count := session.pacingNudgeCount
	history, persist := orchestratorNudgeHistoryEntryFromSession(session)
	a.mu.Unlock()

	a.persistOrchestratorNudgeHistory(history, persist)
	if !a.writeOrchestratorPacingNudge(managed) {
		log.Printf("erun-app: orchestrator %s pacing nudge write failed", id)
		return
	}
	a.emitOrchestratorPacingMarker(serial, count, elapsed, signal, envStuckDetail)
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
//
// It also stamps pacingLastCappedAtUnix, which rearmOrchestratorPacing never
// clears, so a resumed session still reports having been capped.
func (a *App) capOrchestratorPacing(id string, serial int, name string, now time.Time) {
	a.mu.Lock()
	session := a.orchestrators[id]
	if session == nil || session.pacingCapped {
		a.mu.Unlock()
		return
	}
	session.pacingCapped = true
	session.pacingLastCappedAtUnix = now.Unix()
	history, persist := orchestratorNudgeHistoryEntryFromSession(session)
	a.mu.Unlock()
	a.persistOrchestratorNudgeHistory(history, persist)
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
// operator as if it had been honoured.
func (a *App) emitOrchestratorPacingMarker(sessionID, count int, elapsed time.Duration, signal orchestratorPacingActivitySignal, envStuckDetail string) {
	marker := fmt.Sprintf("\r\n\x1b[2;33m── pacing nudge %d/%d sent — %s ──\x1b[0m\r\n",
		count, getOrchestratorWhipConfig().MaxNudges, orchestratorPacingQuietDescription(elapsed, signal, envStuckDetail))
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
// reporting.
//
// envStuckDetail, when non-empty, means a linked environment was still busy on
// this orchestrator's dispatched work when the suppression bound ran out: the
// nudge fires anyway, naming the lane that has not moved.
func orchestratorPacingQuietDescription(elapsed time.Duration, signal orchestratorPacingActivitySignal, envStuckDetail string) string {
	quiet := elapsed.Round(time.Second)
	if envStuckDetail != "" {
		return fmt.Sprintf("waiting on a linked environment (%s) that has not progressed in %s", envStuckDetail, quiet)
	}
	switch signal {
	case orchestratorPacingSignalDied:
		return fmt.Sprintf("no activity report for %s — last report said mid-turn, so the turn may have died without one", quiet)
	case orchestratorPacingSignalNoReport:
		return fmt.Sprintf("no activity report received in the %s since it started", quiet)
	case orchestratorPacingSignalIdle:
		fallthrough
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
