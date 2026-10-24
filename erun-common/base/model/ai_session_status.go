package model

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// AISessionState is an AI session's state as reported by the tool's own hooks;
// output silence cannot distinguish AwaitingInput from Idle.
type AISessionState string

const (
	AISessionStateIdle          AISessionState = "idle"
	AISessionStateBusy          AISessionState = "busy"
	AISessionStateAwaitingInput AISessionState = "awaiting-input"
	AISessionStateExited        AISessionState = "exited"
	AISessionStateOOMKilled     AISessionState = "oom-killed"
)

// AISessionEventKind is a turn-boundary event reported by the AI tool's hooks.
type AISessionEventKind string

const (
	// AISessionEventTurnStart: UserPromptSubmit.
	AISessionEventTurnStart AISessionEventKind = "turn-start"
	// AISessionEventToolUse: Pre/PostToolUse; keeps a long turn fresh.
	AISessionEventToolUse AISessionEventKind = "tool-use"
	// AISessionEventTurnEnd: Stop; resolves to AwaitingInput.
	AISessionEventTurnEnd AISessionEventKind = "turn-end"
	// AISessionEventNotify: Notification (a prompt mid-turn); resolves to AwaitingInput.
	AISessionEventNotify AISessionEventKind = "notify"
	// AISessionEventExit fires once, when the tool's process ends.
	AISessionEventExit AISessionEventKind = "exit"
)

// AISessionExitReasonOOM maps an exit event to AISessionStateOOMKilled.
const AISessionExitReasonOOM = "oom"

var validAISessionEventKinds = []AISessionEventKind{
	AISessionEventTurnStart,
	AISessionEventToolUse,
	AISessionEventTurnEnd,
	AISessionEventNotify,
	AISessionEventExit,
}

// AISessionRecord is the on-disk last event of one session; only that session's hook writes it.
type AISessionRecord struct {
	SessionID  string             `json:"sessionId"`
	Tool       string             `json:"tool,omitempty"`
	Event      AISessionEventKind `json:"event"`
	At         time.Time          `json:"at"`
	ExitCode   *int               `json:"exitCode,omitempty"`
	ExitReason string             `json:"exitReason,omitempty"`
}

// AISessionStatus is the caller-facing view of a session.
type AISessionStatus struct {
	SessionID string         `json:"sessionId"`
	Tool      string         `json:"tool,omitempty"`
	State     AISessionState `json:"state"`
	Reason    string         `json:"reason"`
	// omitzero: no activity omits the field rather than rendering the zero time.
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

// ResolveAISessionStatus derives state from the last event only, never from
// elapsed time, so AwaitingInput persists until another event.
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

// ValidateAISessionID requires a plain path segment so the id cannot escape the
// sessions directory.
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
