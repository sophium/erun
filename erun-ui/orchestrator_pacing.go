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
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Pacing re-states the contract into a session whose activity report has gone
// quiet. Any report renewed at a turn boundary, busy or idle, proves it is not stalled.

// Unconfigured defaults; tests stage candidates off them.
const (
	orchestratorPacingStaleAfter = orchestrator.DefaultWhipStaleAfter
	orchestratorPacingMaxNudges  = orchestrator.DefaultWhipMaxNudges
)

// orchestratorPacingNudgeSettle keeps the text and its carriage return from
// being coalesced into one read. A variable so tests can zero it.
var orchestratorPacingNudgeSettle = 150 * time.Millisecond

// orchestratorPacingNudgeText is the default; config `whip.message` overrides it.
const orchestratorPacingNudgeText = orchestrator.DefaultWhipMessage

// orchestratorPacingActivity is the raw report, without the spinner's TTL.
type orchestratorPacingActivity struct {
	activity orchestratorActivity
	at       time.Time
}

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

// orchestratorPacingCandidate is one orchestrator's decision input. It carries no
// background-shell fact: a long build is the likeliest thing in flight when a turn dies.
type orchestratorPacingCandidate struct {
	alive        bool
	lastActiveAt time.Time
	nudgeCount   int
	capped       bool
	// unmanaged: no session held here; the session may well be alive.
	unmanaged bool
}

type orchestratorPacingDecision int

const (
	orchestratorPacingNone orchestratorPacingDecision = iota
	orchestratorPacingNudge
	orchestratorPacingCap
)

// orchestratorPacingReason is logged so a quiet pane and a suppressed one are distinguishable.
type orchestratorPacingReason string

const (
	orchestratorPacingReasonNotAlive      orchestratorPacingReason = "not-alive"
	orchestratorPacingReasonFresh         orchestratorPacingReason = "fresh"
	orchestratorPacingReasonAlreadyCapped orchestratorPacingReason = "already-capped"
	orchestratorPacingReasonCapCrossed    orchestratorPacingReason = "cap-crossed"
	orchestratorPacingReasonNudge         orchestratorPacingReason = "nudge"
	// Not NotAlive: such sessions are usually alive, just unreachable from here.
	orchestratorPacingReasonUnreachable orchestratorPacingReason = "unreachable-from-transport"
)

// decideOrchestratorPacing is the automatic-pass decision against the live whip config.
func decideOrchestratorPacing(c orchestratorPacingCandidate, now time.Time) (orchestratorPacingDecision, orchestratorPacingReason) {
	return decideOrchestratorWhip(c, now, false)
}

// orchestratorPacingRow is gathered under a.mu before any IO outside the lock.
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
	envs             []configpkg.OrchestratorEnvConfig
	// unmanaged rows are decided and logged but never acted on.
	unmanaged bool
}

// quietMeasured is false when there is no reference point, so the log avoids a
// duration measured from the zero time.
func (r orchestratorPacingRow) quietMeasured(hasReport bool) bool {
	return !r.unmanaged || hasReport
}

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
	// Cover the whole configured population, not just sessions held here.
	for _, row := range a.orchestratorPacingUnmanagedRows(managed) {
		a.reconcileOrchestratorPacingOne(row, now, false)
	}
}

// orchestratorPacingUnreachable reports a configured orchestrator with no session
// here but recent hook activity, so the card can tell "no nudge needed" from
// "no nudge possible". It uses the pacer's own stale bound.
func orchestratorPacingUnreachable(id string, now time.Time) bool {
	report, ok := readOrchestratorPacingActivity(id)
	return ok && now.Sub(report.at) <= getOrchestratorWhipConfig().StaleAfter
}

// orchestratorPacingUnmanagedRows returns a row per configured orchestrator with
// no session here, carrying its last logged reason for once-per-transition logging.
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

// forgetPacedOrchestrators drops remembered lines for orchestrators with a
// session this pass, so they log again if they return to the unpaced population.
func (a *App) forgetPacedOrchestrators(managed map[string]struct{}) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for id := range a.unmanagedPacingReason {
		if _, ok := managed[id]; ok {
			delete(a.unmanagedPacingReason, id)
		}
	}
}

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

// orchestratorPacingActivitySignal lets the marker call a turn died only when the
// last report said busy; idle-then-quiet is the ordinary case.
type orchestratorPacingActivitySignal int

const (
	orchestratorPacingSignalIdle orchestratorPacingActivitySignal = iota
	orchestratorPacingSignalNoReport
	orchestratorPacingSignalDied
)

// orchestratorPacingSignalFor classifies the last report regardless of staleness.
func orchestratorPacingSignalFor(hasReport, lastBusy bool) orchestratorPacingActivitySignal {
	if !hasReport {
		return orchestratorPacingSignalNoReport
	}
	if lastBusy {
		return orchestratorPacingSignalDied
	}
	return orchestratorPacingSignalIdle
}

func (a *App) reconcileOrchestratorPacingOne(row orchestratorPacingRow, now time.Time, explicit bool) (orchestratorPacingDecision, orchestratorPacingReason) {
	lastActiveAt := row.startedAt
	report, ok := readOrchestratorPacingActivity(row.id)
	if ok && report.at.After(lastActiveAt) {
		lastActiveAt = report.at
	}
	// Any report after the last nudge is a turn boundary; requiring Busy would miss
	// a short reply that returns to idle between ticks.
	if ok && report.at.Unix() > row.lastNudgeAtUnix {
		a.rearmOrchestratorPacing(row.id)
		row.nudgeCount = 0
		row.capped = false
	}

	elapsed := now.Sub(lastActiveAt)
	envBusy := orchestratorLinkedEnvBusyStateFor(row.id, row.envs, a.runtime.ActivitySnapshot())
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

// logOrchestratorPacingTransition logs only when the reason changes.
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
	// The id is the stable key; the display name follows only when it differs.
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

// rearmOrchestratorPacing clears the cap budget on fresh activity or operator
// input, leaving the cumulative hover-card counters alone.
func (a *App) rearmOrchestratorPacing(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if session := a.orchestrators[id]; session != nil {
		session.pacingNudgeCount = 0
		session.pacingCapped = false
	}
}

// sendOrchestratorPacingNudge counts the attempt before writing so a failing pty
// cannot loop forever; explicit picks which cumulative counter to bump.
func (a *App) sendOrchestratorPacingNudge(id string, serial int, now time.Time, elapsed time.Duration, signal orchestratorPacingActivitySignal, envStuckDetail string, explicit bool) {
	a.mu.Lock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		a.mu.Unlock()
		return
	}
	if typedRecentlyLocked(managed) {
		// The operator is typing here; skip without spending the cap and retry next tick.
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

// writeOrchestratorPacingNudge submits with a separate, settled carriage return;
// a combined write risks the CR being read as pasted text.
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

// liveSessionOf reads the pty under lock so a concurrent close cannot race the write.
func (a *App) liveSessionOf(managed *managedTerminal) (terminalSession, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if managed.closed || managed.session == nil {
		return nil, false
	}
	return managed.session, true
}

// capOrchestratorPacing latches once per cap so the notice posts once; the
// capped-at stamp survives rearm.
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

// emitOrchestratorPacingMarker announces the nudge in the pane. elapsed is the
// measured quiet period, never the stale-after constant.
func (a *App) emitOrchestratorPacingMarker(sessionID, count int, elapsed time.Duration, signal orchestratorPacingActivitySignal, envStuckDetail string) {
	marker := fmt.Sprintf("\r\n\x1b[2;33m── pacing nudge %d/%d sent — %s ──\x1b[0m\r\n",
		count, getOrchestratorWhipConfig().MaxNudges, orchestratorPacingQuietDescription(elapsed, signal, envStuckDetail))
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// orchestratorPacingQuietDescription adds a died-turn note when the last report
// stayed busy, and names a stuck linked env when envStuckDetail is set.
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

func (a *App) emitOrchestratorPacingCappedMarker(sessionID int) {
	marker := "\r\n\x1b[2;33m── stopped pacing nudges after repeated silence — reply in this pane or restart the orchestrator to resume ──\x1b[0m\r\n"
	a.emitEvent(uimodel.TerminalOutputEvent, terminalOutputPayload{
		SessionID: sessionID,
		Data:      base64.StdEncoding.EncodeToString([]byte(marker)),
	})
}

// orchestratorPacingCappedNotice is for the notification surface, since the pane may be out of view.
func orchestratorPacingCappedNotice(name string) string {
	label := strings.TrimSpace(name)
	if label == "" {
		label = "An orchestrator"
	}
	return fmt.Sprintf("%s stopped answering pacing nudges after %d attempts over about an hour. "+
		"Reply in its pane or restart it to resume.", label, getOrchestratorWhipConfig().MaxNudges)
}
