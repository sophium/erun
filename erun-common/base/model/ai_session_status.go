package model

import (
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
