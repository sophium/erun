package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Persisted beside window-state.json because the activity queue never is.
const interruptedActivityFileName = "interrupted-activity.json"

type interruptedActivityRecord struct {
	ClosedAt time.Time            `json:"closedAt"`
	Entries  []activityQueueEntry `json:"entries"`
}

func defaultInterruptedActivityPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", interruptedActivityFileName)
}

// No-op when empty, so an idle close never clobbers a stale file.
func writeInterruptedActivityRecord(path string, entries []activityQueueEntry) error {
	if path == "" || len(entries) == 0 {
		return nil
	}
	data, err := json.Marshal(interruptedActivityRecord{ClosedAt: currentTime(), Entries: entries})
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

var currentTime = time.Now

// Read-and-delete so the next launch reports it exactly once.
func consumeInterruptedActivityRecord(path string) (interruptedActivityRecord, bool) {
	if path == "" {
		return interruptedActivityRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return interruptedActivityRecord{}, false
	}
	_ = os.Remove(path)
	var record interruptedActivityRecord
	if err := json.Unmarshal(data, &record); err != nil || len(record.Entries) == 0 {
		return interruptedActivityRecord{}, false
	}
	return record, true
}

// ConsumeInterruptedActivityNotice reports and clears work a previous confirmed
// close interrupted.
func (a *App) ConsumeInterruptedActivityNotice() []activityQueueEntry {
	record, ok := consumeInterruptedActivityRecord(a.deps.interruptedActivityPath)
	if !ok {
		return nil
	}
	return record.Entries
}
