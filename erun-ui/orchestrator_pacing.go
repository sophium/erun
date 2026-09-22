package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"sort"
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
// (whipOrchestratorsNow) can read it from a different goroutine than the
// reconciler tick.
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
	// unmanaged marks a candidate this desktop holds no session for at all: a
	// configured orchestrator whose session was started outside it (a terminal,
	// or a previous desktop instance). It is the transport fact Reachable
	// carries — not a statement about the session, which may be perfectly alive
	// and is exactly the case that must not be reported as dead. Zero value is
	// the ordinary, reachable case, so a candidate built without it keeps
	// today's meaning.
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
	// desktop holds no session for, so no nudge can be written into it from
	// here whatever its own session is doing. It is deliberately not
	// orchestratorPacingReasonNotAlive: that is a claim about the session, and
	// the sessions this covers are usually alive and reporting — reporting to a
	// desktop that cannot answer them. Naming the transport rather than the
	// session is the difference between "nothing is running" and "nothing here
	// can reach what is running", and only the second one is true.
	orchestratorPacingReasonUnreachable orchestratorPacingReason = "unreachable-from-transport"
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
		Kind: eruncommon.WhipTargetOrchestrator,
		// The desktop holds this orchestrator's own PTY, so it can write a
		// nudge into it — except for a configured orchestrator it holds no
		// session for, which nothing here can reach (see
		// orchestratorPacingUnmanagedRows).
		Reachable:    !c.unmanaged,
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
// return. WhipReasonUnreachable reaches here only for a configured
// orchestrator whose session this desktop does not hold (see
// orchestratorPacingUnmanagedRows) — every other orchestrator row is one the
// desktop holds the PTY of, which is what makes those candidates reachable.
func orchestratorPacingReasonFromWhip(reason eruncommon.WhipReason) orchestratorPacingReason {
	switch reason {
	case eruncommon.WhipReasonNotAlive:
		return orchestratorPacingReasonNotAlive
	case eruncommon.WhipReasonUnreachable:
		return orchestratorPacingReasonUnreachable
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
	// envs is this orchestrator's configured link scope (orchestrator.go's
	// session.envs), carried through so the env-aware gate only ever consults
	// environments this orchestrator actually linked, never whatever else
	// happens to be running (erun#1699).
	envs []eruncommon.OrchestratorEnvConfig
	// unmanaged marks a configured orchestrator this desktop holds no session
	// for, so it has no PTY to write into, no nudge budget to keep, and no
	// wires that scope could have been read from. Everything that acts on a
	// row is skipped for it; the reconciler still decides and logs for it,
	// which is the whole point (see orchestratorPacingUnmanagedRows).
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
// a nudge count frozen at zero cannot distinguish, and the reason an
// orchestrator started in a terminal used to read exactly like a freshly
// checked one.
//
// It is the read-model's own question, answered against the pacer's own stale
// bound, so "there is a session out there" means here what it means to the
// reconciler. An orchestrator with no session anywhere (an ordinary stopped
// one) reports false: nothing is running outside this desktop, so there is
// nothing to say beyond the "Stopped" the card already shows.
func orchestratorPacingUnreachable(id string, now time.Time) bool {
	report, ok := readOrchestratorPacingActivity(id)
	return ok && now.Sub(report.at) <= getOrchestratorWhipConfig().StaleAfter
}

// orchestratorPacingUnmanagedRows names every configured orchestrator this
// desktop holds no session for, one row each, carrying the reason last logged
// for it so its line obeys the same once-per-transition rule a session-backed
// row's does.
//
// It is what keeps the pacing log's coverage equal to the configured
// population. A session started outside this desktop (in a terminal, or by a
// previous desktop instance) writes the same activity and live-conversation
// records a launched one does — so the desktop reads and shows it — but it has
// no session object here, so it had no row, was never reconciled, and never
// appeared in a log whose whole purpose is to make a quiet pane and a
// suppressed one tell each other apart. An orchestrator that is never
// evaluated is less legible than one that is suppressed, which is the inverse
// of what that log exists for.
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

// orchestratorWhipOutcome is one orchestrator's result from an explicit
// whip-everything pass — the visible record an operator judges the feature
// by: which orchestrator, what was decided, and why.
type orchestratorWhipOutcome struct {
	id       string
	name     string
	decision orchestratorPacingDecision
	reason   orchestratorPacingReason
}

// orchestratorWhipTarget is one orchestrator the whip selection surface can
// offer -- id plus the name a human-facing row renders, gathered without
// deciding or pushing anything.
type orchestratorWhipTarget struct{ id, name string }

// listWhipOrchestratorTargets enumerates every orchestrator eligible to be
// whipped -- every live session plus every persisted config with no session
// yet, deduplicated by id -- the same union whipOrchestratorsNow decides
// against, so "select all orchestrators" and "push the selected
// orchestrators" can never disagree about who exists.
func (a *App) listWhipOrchestratorTargets() []orchestratorWhipTarget {
	rows := a.orchestratorPacingRows()
	seen := make(map[string]struct{}, len(rows))
	targets := make([]orchestratorWhipTarget, 0, len(rows))
	for _, row := range rows {
		targets = append(targets, orchestratorWhipTarget{id: row.id, name: row.name})
		seen[row.id] = struct{}{}
	}
	if configs, err := a.loadOrchestratorConfigs(); err == nil {
		for _, config := range configs {
			if _, ok := seen[config.ID]; ok {
				continue
			}
			targets = append(targets, orchestratorWhipTarget{id: config.ID, name: config.Name})
		}
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].name != targets[j].name {
			return targets[i].name < targets[j].name
		}
		return targets[i].id < targets[j].id
	})
	return targets
}

// whipOrchestratorsNow pushes only the requested orchestrators (by id), each
// still bound by its own cap and pushed now regardless of staleness. Named
// per orchestrator in the return so a caller can report exactly who was
// pushed and who was skipped, and why skipped.
func (a *App) whipOrchestratorsNow(want map[string]struct{}) []orchestratorWhipOutcome {
	refreshOrchestratorWhipConfig()
	now := time.Now()
	rows := a.orchestratorPacingRows()
	outcomes := make([]orchestratorWhipOutcome, 0, len(want))
	whipped := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if _, ok := want[row.id]; !ok {
			continue
		}
		decision, reason := a.reconcileOrchestratorPacingOne(row, now, true)
		outcomes = append(outcomes, orchestratorWhipOutcome{id: row.id, name: row.name, decision: decision, reason: reason})
		whipped[row.id] = struct{}{}
	}
	// orchestratorPacingRows enumerates the sessions this desktop holds, so on
	// its own it answers "who could be nudged", not "who was considered". A
	// requested orchestrator that was never opened has no session and would
	// therefore be absent from the report entirely -- and an omission reads as
	// "not a target", where a skip names its reason. The environment half
	// already lists configs for this reason, as does ListWhipOrchestratorCandidates
	// on the CLI/MCP transports; this keeps the desktop honest the same way.
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		// Report what is known rather than nothing: the sessions above are still
		// a truthful partial answer, and swallowing them to signal a config read
		// failure would trade one silence for a larger one.
		return outcomes
	}
	for _, config := range configs {
		if _, ok := want[config.ID]; !ok {
			continue
		}
		if _, ok := whipped[config.ID]; ok {
			continue
		}
		outcomes = append(outcomes, orchestratorWhipOutcome{
			id:       config.ID,
			name:     config.Name,
			decision: orchestratorPacingNone,
			reason:   orchestratorPacingReasonNotAlive,
		})
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
//
// quietKnown is whether the elapsed period means anything: a row the desktop
// holds no session for and that has never reported has no reference point at
// all, and reporting a duration measured from the zero time would invent an
// outage of fifty-odd years rather than admit nothing is known.
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
	// The id leads because it is the stable key every other surface uses — the
	// nudge history state file, ERUN_ORCHESTRATOR_ID, and review-directory
	// lookups. The display name is allowed to differ from it, so naming only the
	// name left a log line and its state record uncorrelatable without reading
	// config.yaml. The name follows parenthetically, but only when it adds
	// information; the common id == name case stays exactly as it read before.
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
// It deliberately does not touch pacingAutoNudgeCount/pacingWhipCount or
// their timestamps: those are the cumulative history a hover card reports,
// not the cap's budget, and a session that answers must still be reportable
// as having been nudged.
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
// when a linked environment's own busy lease was still active but the
// suppression bound was crossed anyway (erun#1699) — the ordinary case leaves
// it empty and the marker reads exactly as it did before.
//
// explicit distinguishes an operator-triggered whip from the automatic
// pacer, both of which land here: it decides which cumulative history
// counter this delivery adds to (pacingAutoNudgeCount vs pacingWhipCount) so
// the two can be reported separately, without changing pacingNudgeCount's own
// cap bookkeeping, which both paths share exactly as before.
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
// It also stamps pacingLastCappedAtUnix, the cumulative record of when this
// session last hit the cap: unlike pacingCapped itself, rearmOrchestratorPacing
// never clears it, so a session that later resumes still reports having been
// capped rather than reading as if it never happened.
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
// operator as if it had been honoured (erun#1376).
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
// reporting. Without this, both look identical in the pane and the operator
// has to go read a transcript to tell them apart (erun#1376).
//
// envStuckDetail, when non-empty, means a linked environment was still busy on
// this orchestrator's own dispatched work when the suppression bound ran out
// (erun#1699): the nudge fires anyway, but the description names the lane that
// has not moved rather than reading as an ordinary silent pane.
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
