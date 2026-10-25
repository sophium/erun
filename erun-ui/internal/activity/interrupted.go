package activity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Persisted beside window-state.json because the activity queue never is.
const interruptedActivityFileName = "interrupted-activity.json"

// InterruptedRecord is the work a confirmed close interrupted.
type InterruptedRecord struct {
	ClosedAt time.Time `json:"closedAt"`
	Entries  []Entry   `json:"entries"`
}

// DefaultInterruptedActivityPath is the record's place beside window-state.json.
func DefaultInterruptedActivityPath() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun", interruptedActivityFileName)
}

// RecordInterrupted persists entries as the work a confirmed close interrupts.
func (s *Service) RecordInterrupted(entries []Entry) error {
	return writeInterruptedActivityRecord(s.d.InterruptedActivityPath(), entries)
}

// No-op when empty, so an idle close never clobbers a stale file.
func writeInterruptedActivityRecord(path string, entries []Entry) error {
	if path == "" || len(entries) == 0 {
		return nil
	}
	data, err := json.Marshal(InterruptedRecord{ClosedAt: currentTime(), Entries: entries})
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
func consumeInterruptedActivityRecord(path string) (InterruptedRecord, bool) {
	if path == "" {
		return InterruptedRecord{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return InterruptedRecord{}, false
	}
	_ = os.Remove(path)
	var record InterruptedRecord
	if err := json.Unmarshal(data, &record); err != nil || len(record.Entries) == 0 {
		return InterruptedRecord{}, false
	}
	return record, true
}

// ConsumeInterruptedActivityNotice reports and clears work a previous confirmed
// close interrupted.
func (s *Service) ConsumeInterruptedActivityNotice() []Entry {
	record, ok := consumeInterruptedActivityRecord(s.d.InterruptedActivityPath())
	if !ok {
		return nil
	}
	return record.Entries
}

// Running lists the work a window close would interrupt. It leaves out queued
// entries and tabs still being established, which would otherwise prompt on the
// idle path after launch.
func (s *Service) Running() []Entry {
	var running []Entry
	for _, entry := range s.queue.list() {
		if entry.Status == StatusRunning && !isGatedSessionSetupEntry(entry) {
			running = append(running, entry)
		}
	}
	return running
}
