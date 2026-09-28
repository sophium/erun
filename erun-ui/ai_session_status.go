package main

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// ai_session_status.go is the desktop's read of the structured AI-session
// status model (erun-common/ai_session_status.go) for an environment's AI tab.
//
// The sidebar badge used to be a pure function of PTY output volume: five
// seconds of output latched "working" on, three seconds of silence latched it
// off. Volume cannot represent the state the model exists to carry. A session
// blocked on the human prints nothing, which is exactly what a finished one
// looks like from the stream; and the pod heartbeat held the latch the other
// way, because a live `claude` process kept the row spinning after its turn had
// ended — so an Agent waiting on an answer read as working for as long as its
// process lived, which is the one thing an operator most needs to see.
//
// So the badge asks the tool. The environment's own `erun activity ai-session
// status` resolves busy / awaiting-input / exited / oom-killed from the last
// turn-boundary event the tool's own hooks reported, with no elapsed-time input
// — which is what makes "waiting on you" representable at all. Output volume
// remains the answer only where the model has nothing to say: an environment
// whose AI tool has never reported a turn boundary keeps the volume latch it
// has always had, and so does one too old to have the verb.
//
// Resolving the state is not the same as showing it, so the badge carries both
// answers rather than one: the spinner means work in flight, and a turn
// boundary — which releases that spinner, correctly — also raises its own
// "waiting on you" state. A badge carrying only "busy" renders the state the
// model exists for as its opposite: the row goes quiet exactly when the
// operator is being asked for something.
//
// The read goes over the runtime pod exec the heartbeat already uses rather
// than the MCP edge, for the reason the heartbeat does: kubectl exec works
// while the edge is down, and a dead edge is exactly when a session is most
// likely to look stale.

// aiSessionStatusTTL bounds how long a reading stays applicable. Matched to the
// session heartbeat's own bound: the same tick refreshes both, so a reading and
// the pod observation it is weighed against are never far apart in time, and a
// reading the desktop can no longer refresh stops having a say rather than
// pinning a row forever.
const aiSessionStatusTTL = 3 * sessionHeartbeatInterval

// aiSessionStatusTimeout bounds one read. It is a pod exec plus a CLI start, the
// same shape and cost as the heartbeat probe, and it runs on the same tick.
const aiSessionStatusTimeout = 5 * time.Second

// aiSessionStatusMarker brackets the CLI's JSON in the combined output the pod
// exec returns. kubectlText merges stderr into stdout, so the JSON has to be
// delimited rather than parsed out of the whole stream.
const aiSessionStatusMarker = "erun-ai-sessions"

// aiSessionReading is one environment's last resolved reading, with when it was
// taken. sessions is every session the tool has reported for the environment,
// including ones that have exited: which of them can speak for the desktop's
// own tab is decided per tab, at use (aiSessionReadingForTab).
type aiSessionReading struct {
	observedAt time.Time
	sessions   []eruncommon.AISessionStatus
}

// aiSessionStatusScript asks the pod's own erun for the environment's resolved
// AI-session status. erun's stderr is discarded so a warning cannot land inside
// the JSON, and a pod too old to have the verb prints nothing between the
// markers, which reads as no reading rather than as an empty one.
func aiSessionStatusScript(selection uiSelection) string {
	return fmt.Sprintf(
		"printf '%s\\tbegin\\n'; erun activity ai-session status --tenant %s --environment %s --output json 2>/dev/null; printf '%s\\tend\\n'",
		aiSessionStatusMarker,
		shellSingleQuote(selection.Tenant),
		shellSingleQuote(selection.Environment),
		aiSessionStatusMarker,
	)
}

// parseAISessionStatuses reads the marked block out of a pod exec's combined
// output. A block that is missing, or that does not hold a JSON array, reports
// ok=false: a failed read is evidence of nothing, and must not be mistaken for
// a reading that says no session is working.
func parseAISessionStatuses(probe string) ([]eruncommon.AISessionStatus, bool) {
	block, ok := markedBlock(probe, aiSessionStatusMarker)
	if !ok {
		return nil, false
	}
	// Trace output around the array is dropped rather than failing the read;
	// only what is between the first '[' and the last ']' is the payload.
	start := strings.Index(block, "[")
	end := strings.LastIndex(block, "]")
	if start < 0 || end < start {
		return nil, false
	}
	var statuses []eruncommon.AISessionStatus
	if err := json.Unmarshal([]byte(block[start:end+1]), &statuses); err != nil {
		return nil, false
	}
	return statuses, true
}

// markedBlock extracts what a script wrote between its begin and end markers.
func markedBlock(output, marker string) (string, bool) {
	_, rest, ok := strings.Cut(output, marker+"\tbegin")
	if !ok {
		return "", false
	}
	if end := strings.Index(rest, marker+"\tend"); end >= 0 {
		return rest[:end], true
	}
	// An unterminated block is still a block: the command ran and printed, and
	// only its closing marker is missing.
	return rest, true
}

// reconcileAISessionStatusesOnce refreshes every environment's reading and
// applies it to that environment's AI tabs. It is called from the heartbeat
// ticker, after the pod observations are fresh, so a decision made here is
// weighed against this tick's view of what the pod is running.
//
// Only environments with an open AI tab are read. The badge exists for that
// tab, an environment without one has no row to spin, and each read costs a pod
// exec.
//
// The deadlock this cannot have: it holds no lock while reading. The read is
// network-bound, and taking a.mu across it would stall every other session
// operation behind a cluster round trip.
func (a *App) reconcileAISessionStatusesOnce() {
	tabs := a.openAITabs()
	read := make(map[string]struct{}, len(tabs))
	for _, managed := range tabs {
		key := selectionKey(managed.selection)
		if _, done := read[key]; !done {
			read[key] = struct{}{}
			a.readAISessionStatusOnce(managed.selection)
		}
		a.applyAISessionReading(managed)
	}
}

// openAITabs snapshots the AI tabs the badge can be drawn for, in serial order
// so a pass is reproducible.
func (a *App) openAITabs() []*managedTerminal {
	a.mu.Lock()
	defer a.mu.Unlock()
	tabs := make([]*managedTerminal, 0, len(a.sessions))
	for _, managed := range a.sessions {
		if managed == nil || managed.closed || !aiActivityKind(managed.kind) {
			continue
		}
		tabs = append(tabs, managed)
	}
	slices.SortFunc(tabs, func(a, b *managedTerminal) int { return a.serial - b.serial })
	return tabs
}

// readAISessionStatusOnce stores one environment's reading. A failed read
// leaves the previous reading in place to age out of its TTL rather than
// dropping it: an unreachable pod is not evidence that a running turn ended.
func (a *App) readAISessionStatusOnce(selection uiSelection) {
	ctx, cancel := context.WithTimeout(a.backgroundContext(), aiSessionStatusTimeout)
	defer cancel()
	output, err := a.execInRuntimePod(ctx, selection, aiSessionStatusScript(selection))
	if err != nil {
		return
	}
	sessions, ok := parseAISessionStatuses(output)
	if !ok {
		return
	}
	a.recordAISessionReading(selection, sessions)
}

// recordAISessionReading stores one environment's resolved reading.
func (a *App) recordAISessionReading(selection uiSelection, sessions []eruncommon.AISessionStatus) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.aiSessionReadings == nil {
		a.aiSessionReadings = make(map[string]aiSessionReading)
	}
	a.aiSessionReadings[selectionKey(selection)] = aiSessionReading{
		observedAt: time.Now(),
		sessions:   sessions,
	}
}

// aiSessionReadingForLocked returns the environment's reading while it is
// fresh. A reading the desktop can no longer refresh is treated as absent, so
// an unreachable pod can never pin a row forever. Caller holds a.mu.
func (a *App) aiSessionReadingForLocked(selection uiSelection) (aiSessionReading, bool) {
	reading, ok := a.aiSessionReadings[selectionKey(selection)]
	if !ok || time.Since(reading.observedAt) > aiSessionStatusTTL {
		return aiSessionReading{}, false
	}
	return reading, true
}

// aiSessionTabEvidence is what the environment's AI-session records say about
// one tab.
type aiSessionTabEvidence struct {
	// reported says the tool has said something about this tab at all.
	// reported=false means the volume latch keeps its say.
	reported bool
	// busy says a turn is in flight.
	busy bool
	// awaitingInput says the tool is blocked on the operator: its turn ended, or
	// a permission or a question is pending mid-turn. It is the state the volume
	// rule structurally cannot produce — a session waiting on a human prints
	// nothing, which is what a finished one looks like from the stream — and the
	// reason the badge asks the tool rather than the PTY.
	awaitingInput bool
}

// aiSessionEvidenceForTab answers what the tool says about one tab. A tab with
// no report keeps the volume latch it has always had.
func (a *App) aiSessionEvidenceForTab(managed *managedTerminal) aiSessionTabEvidence {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aiSessionEvidenceForTabLocked(managed)
}

// aiSessionEvidenceForTabLocked is aiSessionEvidenceForTab for a caller already
// holding a.mu. Caller holds a.mu.
func (a *App) aiSessionEvidenceForTabLocked(managed *managedTerminal) aiSessionTabEvidence {
	if managed == nil {
		return aiSessionTabEvidence{}
	}
	reading, ok := a.aiSessionReadingForLocked(managed.selection)
	if !ok {
		return aiSessionTabEvidence{}
	}
	return aiSessionReadingForTab(reading, managed.startedAt)
}

// aiSessionReadingForTab attributes a reading to one tab. Only records the tool
// wrote since this tab's session started can be about it, and that comparison
// is what is left of staleness here — the model itself resolves state with no
// elapsed-time input by design, so it can say "still awaiting input" an hour
// later, correctly. What it cannot say is *whose* turn it was: a record an
// earlier session left behind would otherwise pin this row for good, and "the
// tool reported a turn boundary before this tab existed" is not evidence about
// the agent the operator is watching now. Such a record is evidence neither
// way, so the volume latch decides — which is exactly the pre-existing
// behaviour rather than a new silence.
func aiSessionReadingForTab(reading aiSessionReading, startedAt time.Time) aiSessionTabEvidence {
	var evidence aiSessionTabEvidence
	for _, status := range reading.sessions {
		if status.LastActivity.IsZero() || !status.LastActivity.After(startedAt) {
			continue
		}
		evidence.reported = true
		switch status.State {
		case eruncommon.AISessionStateBusy:
			evidence.busy = true
		case eruncommon.AISessionStateAwaitingInput:
			evidence.awaitingInput = true
		}
	}
	// Work in flight outranks a sibling session's turn boundary. One environment
	// can hold more than one AI conversation, and the desktop draws one badge for
	// all of them: a row that said "waiting on you" while some other conversation
	// in it was mid-turn would be reporting the quieter of two truths. Busy also
	// has to be the answer the volume rule and the report agree on, since either
	// can raise the spinner.
	if evidence.busy {
		evidence.awaitingInput = false
	}
	return evidence
}

// applyAISessionReading turns one tab's reading into the sidebar signals. A
// reading that says nothing about this tab is left alone entirely.
func (a *App) applyAISessionReading(managed *managedTerminal) {
	evidence := a.aiSessionEvidenceForTab(managed)
	if !evidence.reported {
		return
	}
	// Where the tool reports its own state, it owns both signals. A turn in
	// flight is the working spinner without the five seconds of output the
	// volume rule needs — the tool said its turn started, which is the thing the
	// volume rule was approximating. A turn boundary is the other answer, and it
	// is not "nothing is happening": the tool has stopped working and is waiting
	// on the operator, which is its own state to render rather than the absence
	// of the spinner. The heartbeat's "the program is running" is no
	// counter-argument to either — a process waiting for input is running.
	a.applyAIActivityReport(managed, evidence)
}
