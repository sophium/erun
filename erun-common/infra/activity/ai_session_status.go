package activity

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// RecordAISessionEvent replaces the session's previous event; only the latest decides state.
func RecordAISessionEvent(params model.AISessionEventParams) error {
	tenant := strings.TrimSpace(params.Tenant)
	environment := strings.TrimSpace(params.Environment)
	sessionID := strings.TrimSpace(params.SessionID)
	if err := core.ErrMissingTenantOrEnvironment("record AI session event", tenant, environment); err != nil {
		return err
	}
	if err := core.ValidateAISessionID(sessionID); err != nil {
		return err
	}
	if !core.ValidAISessionEventKind(params.Event) {
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

// previouslyReportedAISessionTool keeps the tool name when a later event omits it; lookup
// failures read as no prior tool.
func previouslyReportedAISessionTool(tenant, environment, sessionID string) string {
	existing, ok, err := loadAISessionRecord(tenant, environment, sessionID)
	if err != nil || !ok {
		return ""
	}
	return existing.Tool
}

// LoadAISessionStatus reads a session with no recorded event as Idle.
func LoadAISessionStatus(tenant, environment, sessionID string) (model.AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	sessionID = strings.TrimSpace(sessionID)
	if err := core.ErrMissingTenantOrEnvironment("resolve AI session status", tenant, environment); err != nil {
		return model.AISessionStatus{}, err
	}
	if err := core.ValidateAISessionID(sessionID); err != nil {
		return model.AISessionStatus{}, err
	}
	record, ok, err := loadAISessionRecord(tenant, environment, sessionID)
	if err != nil {
		return model.AISessionStatus{}, err
	}
	if !ok {
		return model.AISessionStatus{SessionID: sessionID, State: model.AISessionStateIdle, Reason: "no AI session activity recorded for this id"}, nil
	}
	return core.ResolveAISessionStatus(record), nil
}

// LoadAISessionStatuses returns every session sorted by id.
func LoadAISessionStatuses(tenant, environment string) ([]model.AISessionStatus, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("list AI session statuses", tenant, environment); err != nil {
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
		statuses = append(statuses, core.ResolveAISessionStatus(record))
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].SessionID < statuses[j].SessionID })
	return statuses, nil
}

// readAISessionRecordFile skips bad files rather than failing the listing.
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
