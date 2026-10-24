package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AISessionState is what a caller actually wants to know about an AI CLI
// session: is it thinking, is it waiting on the human, or is it gone.
//
// A PTY output-volume heuristic (busy while bytes keep arriving, idle after a
// few seconds of silence) cannot represent AwaitingInput: a session waiting on
// a human produces no output at all, which is exactly what Idle also looks
// like. The two are only distinguishable from a direct signal about which
// state the tool is actually in, which is what AISessionEventKind carries.
type AISessionState string

const (
	AISessionStateIdle          AISessionState = "idle"
	AISessionStateBusy          AISessionState = "busy"
	AISessionStateAwaitingInput AISessionState = "awaiting-input"
	AISessionStateExited        AISessionState = "exited"
	AISessionStateOOMKilled     AISessionState = "oom-killed"
)

// AISessionEventKind is a turn-boundary signal the AI tool itself reports,
// each mapped to the hook event that fires it in Claude Code / Codex: a
// session reports its own state rather than having it guessed from output.
type AISessionEventKind string

const (
	// AISessionEventTurnStart fires when the human hands control to the tool
	// (Claude Code's UserPromptSubmit, Codex's equivalent turn start).
	AISessionEventTurnStart AISessionEventKind = "turn-start"
	// AISessionEventToolUse fires while the tool is still working a turn
	// (PreToolUse/PostToolUse). It keeps a long turn's state fresh so a slow
	// tool call in the middle of it does not read as stale.
	AISessionEventToolUse AISessionEventKind = "tool-use"
	// AISessionEventTurnEnd fires when the tool returns control to the human
	// (Claude Code's Stop). This is the direct signal for AwaitingInput.
	AISessionEventTurnEnd AISessionEventKind = "turn-end"
	// AISessionEventNotify fires when the tool is explicitly blocked on the
	// human mid-turn - a permission prompt or a clarifying question (Claude
	// Code's Notification). Also resolves to AwaitingInput.
	AISessionEventNotify AISessionEventKind = "notify"
	// AISessionEventExit fires once, when the tool's process ends.
	AISessionEventExit AISessionEventKind = "exit"
)

// AISessionExitReasonOOM is the ExitReason value that maps an exit event to
// AISessionStateOOMKilled instead of a plain AISessionStateExited. Detecting
// the OOM kill itself (cgroup memory.events, a dmesg scan) is a wiring concern
// for whatever process observes the exit; this model only records what it is
// told.
const AISessionExitReasonOOM = "oom"

var validAISessionEventKinds = []AISessionEventKind{
	AISessionEventTurnStart,
	AISessionEventToolUse,
	AISessionEventTurnEnd,
	AISessionEventNotify,
	AISessionEventExit,
}

// AISessionRecord is the on-disk state for one AI session: the last event it
// reported, and when. One writer per session file, the session's own hook
// invocation, so there is never a competing update to reconcile.
type AISessionRecord struct {
	SessionID  string             `json:"sessionId"`
	Tool       string             `json:"tool,omitempty"`
	Event      AISessionEventKind `json:"event"`
	At         time.Time          `json:"at"`
	ExitCode   *int               `json:"exitCode,omitempty"`
	ExitReason string             `json:"exitReason,omitempty"`
}

// AISessionStatus is the resolved, caller-facing view of an AI session: the
// read model derived from the last recorded event, never from silence.
type AISessionStatus struct {
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool,omitempty"`
	State     AISessionState `json:"state"`
	Reason    string         `json:"reason"`
	// `omitzero` rather than `omitempty`: a session that has recorded no
	// activity carries no lastActivity field at all, instead of Go's zero
	// instant rendered as though it were a real time. Same rule as the idle
	// markers' timestamps, so a consumer reads one convention.
	LastActivity time.Time `json:"lastActivity,omitzero"`
	ExitCode     *int      `json:"exitCode,omitempty"`
}

// AISessionEventParams is the input to RecordAISessionEvent.
type AISessionEventParams struct {
	Tenant      string
	Environment string
	SessionID   string
	Tool        string
	Event       AISessionEventKind
	ExitCode    *int
	ExitReason  string
	Now         time.Time
}

// ResolveAISessionStatus derives the current state purely from the last
// reported event and its own carried fields - never from how long it has
// been since that event, which is the property that makes AwaitingInput
// representable at all. A session that reported TurnEnd an hour ago is still
// AwaitingInput: nothing said otherwise since.
func ResolveAISessionStatus(record AISessionRecord) AISessionStatus {
	status := AISessionStatus{
		SessionID:    record.SessionID,
		Tool:         record.Tool,
		LastActivity: record.At,
		ExitCode:     record.ExitCode,
	}
	switch record.Event {
	case AISessionEventExit:
		if strings.EqualFold(strings.TrimSpace(record.ExitReason), AISessionExitReasonOOM) {
			status.State = AISessionStateOOMKilled
			status.Reason = "the tool's process was killed by an out-of-memory event"
		} else {
			status.State = AISessionStateExited
			status.Reason = exitReason(record)
		}
	case AISessionEventTurnEnd:
		status.State = AISessionStateAwaitingInput
		status.Reason = "finished its turn and is waiting for your next message"
	case AISessionEventNotify:
		status.State = AISessionStateAwaitingInput
		status.Reason = "is waiting on you: a permission or a question is pending"
	case AISessionEventTurnStart, AISessionEventToolUse:
		status.State = AISessionStateBusy
		status.Reason = "working"
	default:
		status.State = AISessionStateIdle
		status.Reason = "no AI session activity recorded for this id"
	}
	return status
}

func exitReason(record AISessionRecord) string {
	if reason := strings.TrimSpace(record.ExitReason); reason != "" {
		return reason
	}
	if record.ExitCode != nil {
		return fmt.Sprintf("exited with code %d", *record.ExitCode)
	}
	return "exited"
}

// ValidateAISessionID rejects anything that is not a plain path segment, so a
// session id cannot be used to escape the AI-sessions directory it is filed
// under.
func ValidateAISessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("AI session id is required")
	}
	if sessionID != filepath.Base(sessionID) || sessionID == "." || sessionID == ".." {
		return fmt.Errorf("invalid AI session id %q", sessionID)
	}
	return nil
}

func ValidAISessionEventKind(kind AISessionEventKind) bool {
	for _, candidate := range validAISessionEventKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
