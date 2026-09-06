package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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
	SessionID    string         `json:"sessionId"`
	Tool         string         `json:"tool,omitempty"`
	State        AISessionState `json:"state"`
	Reason       string         `json:"reason"`
	LastActivity time.Time      `json:"lastActivity,omitempty"`
	ExitCode     *int           `json:"exitCode,omitempty"`
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

// RecordAISessionEvent persists the AI tool's own report of a turn-boundary
// event. It replaces the previous event for the session outright: only the
// most recent event decides the resolved state, so there is nothing to
// reconcile across a history of stale ones.
func RecordAISessionEvent(params AISessionEventParams) error {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	sessionID := strings.TrimSpace(params.SessionID)
	if err := errMissingTenantOrEnvironment("record AI session event", tenant, environment); err != nil {
		return err
	}
	if err := validateAISessionID(sessionID); err != nil {
		return err
	}
	if !validAISessionEventKind(params.Event) {
		return fmt.Errorf("unsupported AI session event %q", params.Event)
	}
	now := params.Now
	if now.IsZero() {
		now = time.Now()
	}

	dir, err := aiSessionDir(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	record := AISessionRecord{
		SessionID:  sessionID,
		Tool:       strings.TrimSpace(params.Tool),
		Event:      params.Event,
		At:         now,
		ExitCode:   params.ExitCode,
		ExitReason: strings.TrimSpace(params.ExitReason),
	}
	if record.Tool == "" {
		record.Tool = previouslyReportedAISessionTool(tenant, environment, sessionID)
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(aiSessionPath(dir, sessionID), data, 0o644)
}

// previouslyReportedAISessionTool carries the tool name forward from an
// earlier event when a later one (e.g. a bare exit) omits it, so the tool
// column of a resolved status does not go blank mid-session. Any lookup
// failure is silently treated as "no prior tool" rather than failing the
// event this call is trying to record.
func previouslyReportedAISessionTool(tenant, environment, sessionID string) string {
	existing, ok, err := loadAISessionRecord(tenant, environment, sessionID)
	if err != nil || !ok {
		return ""
	}
	return existing.Tool
}

// LoadAISessionStatus resolves the current status for one session. A session
// with no recorded event at all reads as Idle - there has never been an AI
// session running under this id - rather than an error.
func LoadAISessionStatus(tenant, environment, sessionID string) (AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	sessionID = strings.TrimSpace(sessionID)
	if err := errMissingTenantOrEnvironment("resolve AI session status", tenant, environment); err != nil {
		return AISessionStatus{}, err
	}
	if err := validateAISessionID(sessionID); err != nil {
		return AISessionStatus{}, err
	}
	record, ok, err := loadAISessionRecord(tenant, environment, sessionID)
	if err != nil {
		return AISessionStatus{}, err
	}
	if !ok {
		return AISessionStatus{SessionID: sessionID, State: AISessionStateIdle, Reason: "no AI session activity recorded for this id"}, nil
	}
	return ResolveAISessionStatus(record), nil
}

// LoadAISessionStatuses resolves every session recorded for an environment,
// sorted by session id for a stable, diffable listing.
func LoadAISessionStatuses(tenant, environment string) ([]AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("list AI session statuses", tenant, environment); err != nil {
		return nil, err
	}
	dir, err := aiSessionDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []AISessionStatus{}, nil
		}
		return nil, err
	}

	statuses := make([]AISessionStatus, 0, len(entries))
	for _, entry := range entries {
		record, ok := readAISessionRecordFile(filepath.Join(dir, entry.Name()))
		if !ok {
			continue
		}
		statuses = append(statuses, ResolveAISessionStatus(record))
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].SessionID < statuses[j].SessionID })
	return statuses, nil
}

// readAISessionRecordFile reads one session file for LoadAISessionStatuses's
// directory scan. A non-JSON entry, an unreadable file, or a malformed one is
// silently skipped rather than failing the whole listing over one bad file.
func readAISessionRecordFile(path string) (AISessionRecord, bool) {
	if !strings.HasSuffix(path, ".json") {
		return AISessionRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return AISessionRecord{}, false
	}
	var record AISessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return AISessionRecord{}, false
	}
	return record, true
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

func loadAISessionRecord(tenant, environment, sessionID string) (AISessionRecord, bool, error) {
	dir, err := aiSessionDir(tenant, environment)
	if err != nil {
		return AISessionRecord{}, false, err
	}
	data, err := os.ReadFile(aiSessionPath(dir, sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return AISessionRecord{}, false, nil
		}
		return AISessionRecord{}, false, err
	}
	var record AISessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return AISessionRecord{}, false, err
	}
	return record, true, nil
}

func aiSessionDir(tenant, environment string) (string, error) {
	dir, err := EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ai-sessions"), nil
}

func aiSessionPath(dir, sessionID string) string {
	return filepath.Join(dir, sessionID+".json")
}

// validateAISessionID rejects anything that is not a plain path segment, so a
// session id cannot be used to escape the AI-sessions directory it is filed
// under.
func validateAISessionID(sessionID string) error {
	if sessionID == "" {
		return fmt.Errorf("AI session id is required")
	}
	if sessionID != filepath.Base(sessionID) || sessionID == "." || sessionID == ".." {
		return fmt.Errorf("invalid AI session id %q", sessionID)
	}
	return nil
}

func validAISessionEventKind(kind AISessionEventKind) bool {
	for _, candidate := range validAISessionEventKinds {
		if kind == candidate {
			return true
		}
	}
	return false
}
