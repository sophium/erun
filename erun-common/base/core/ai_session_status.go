package core

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

var validAISessionEventKinds = []model.AISessionEventKind{
	model.AISessionEventTurnStart,
	model.AISessionEventToolUse,
	model.AISessionEventTurnEnd,
	model.AISessionEventNotify,
	model.AISessionEventExit,
}

// ResolveAISessionStatus derives state from the last event only, never from
// elapsed time, so AwaitingInput persists until another event.
func ResolveAISessionStatus(record model.AISessionRecord) model.AISessionStatus {
	status := model.AISessionStatus{
		SessionID:    record.SessionID,
		Tool:         record.Tool,
		LastActivity: record.At,
		ExitCode:     record.ExitCode,
	}
	switch record.Event {
	case model.AISessionEventExit:
		if strings.EqualFold(strings.TrimSpace(record.ExitReason), model.AISessionExitReasonOOM) {
			status.State = model.AISessionStateOOMKilled
			status.Reason = "the tool's process was killed by an out-of-memory event"
		} else {
			status.State = model.AISessionStateExited
			status.Reason = exitReason(record)
		}
	case model.AISessionEventTurnEnd:
		status.State = model.AISessionStateAwaitingInput
		status.Reason = "finished its turn and is waiting for your next message"
	case model.AISessionEventNotify:
		status.State = model.AISessionStateAwaitingInput
		status.Reason = "is waiting on you: a permission or a question is pending"
	case model.AISessionEventTurnStart, model.AISessionEventToolUse:
		status.State = model.AISessionStateBusy
		status.Reason = "working"
	default:
		status.State = model.AISessionStateIdle
		status.Reason = "no AI session activity recorded for this id"
	}
	return status
}

func exitReason(record model.AISessionRecord) string {
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

func ValidAISessionEventKind(kind model.AISessionEventKind) bool {
	for _, candidate := range validAISessionEventKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
