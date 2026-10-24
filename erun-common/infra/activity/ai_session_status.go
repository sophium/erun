package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// RecordAISessionEvent persists the AI tool's own report of a turn-boundary
// event. It replaces the previous event for the session outright: only the
// most recent event decides the resolved state, so there is nothing to
// reconcile across a history of stale ones.
func RecordAISessionEvent(params model.AISessionEventParams) error {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	sessionID := strings.TrimSpace(params.SessionID)
	if err := model.ErrMissingTenantOrEnvironment("record AI session event", tenant, environment); err != nil {
		return err
	}
	if err := model.ValidateAISessionID(sessionID); err != nil {
		return err
	}
	if !model.ValidAISessionEventKind(params.Event) {
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

	record := model.AISessionRecord{
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
func LoadAISessionStatus(tenant, environment, sessionID string) (model.AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	sessionID = strings.TrimSpace(sessionID)
	if err := model.ErrMissingTenantOrEnvironment("resolve AI session status", tenant, environment); err != nil {
		return model.AISessionStatus{}, err
	}
	if err := model.ValidateAISessionID(sessionID); err != nil {
		return model.AISessionStatus{}, err
	}
	record, ok, err := loadAISessionRecord(tenant, environment, sessionID)
	if err != nil {
		return model.AISessionStatus{}, err
	}
	if !ok {
		return model.AISessionStatus{SessionID: sessionID, State: model.AISessionStateIdle, Reason: "no AI session activity recorded for this id"}, nil
	}
	return model.ResolveAISessionStatus(record), nil
}

// LoadAISessionStatuses resolves every session recorded for an environment,
// sorted by session id for a stable, diffable listing.
func LoadAISessionStatuses(tenant, environment string) ([]model.AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := model.ErrMissingTenantOrEnvironment("list AI session statuses", tenant, environment); err != nil {
		return nil, err
	}
	dir, err := aiSessionDir(tenant, environment)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []model.AISessionStatus{}, nil
		}
		return nil, err
	}

	statuses := make([]model.AISessionStatus, 0, len(entries))
	for _, entry := range entries {
		record, ok := readAISessionRecordFile(filepath.Join(dir, entry.Name()))
		if !ok {
			continue
		}
		statuses = append(statuses, model.ResolveAISessionStatus(record))
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].SessionID < statuses[j].SessionID })
	return statuses, nil
}

// readAISessionRecordFile reads one session file for LoadAISessionStatuses's
// directory scan. A non-JSON entry, an unreadable file, or a malformed one is
// silently skipped rather than failing the whole listing over one bad file.
func readAISessionRecordFile(path string) (model.AISessionRecord, bool) {
	if !strings.HasSuffix(path, ".json") {
		return model.AISessionRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return model.AISessionRecord{}, false
	}
	var record model.AISessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return model.AISessionRecord{}, false
	}
	return record, true
}

func loadAISessionRecord(tenant, environment, sessionID string) (model.AISessionRecord, bool, error) {
	dir, err := aiSessionDir(tenant, environment)
	if err != nil {
		return model.AISessionRecord{}, false, err
	}
	data, err := os.ReadFile(aiSessionPath(dir, sessionID))
	if err != nil {
		if os.IsNotExist(err) {
			return model.AISessionRecord{}, false, nil
		}
		return model.AISessionRecord{}, false, err
	}
	var record model.AISessionRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return model.AISessionRecord{}, false, err
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
