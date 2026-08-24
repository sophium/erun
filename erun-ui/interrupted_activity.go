package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// interruptedActivityFileName holds the record of work a confirmed
// close-anyway interrupted. It lives beside window-state.json (same
// directory, same load-once-and-clear shape as the orchestrator restart
// hand-off) rather than in the activity queue, which is never persisted.
const interruptedActivityFileName = "interrupted-activity.json"

// interruptedActivityRecord is what ConfirmWindowClose persists: the entries
// that were running when the operator chose to close anyway, and when.
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

// writeInterruptedActivityRecord is a no-op when there is nothing to record,
// so a close with an idle queue never creates or clobbers a stale file.
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

// currentTime is a seam so tests can pin the timestamp without a fake clock
// threaded through the whole close-confirmation path.
var currentTime = time.Now

// consumeInterruptedActivityRecord reads and deletes the record so the next
// launch reports it exactly once, mirroring
// readAndClearOrchestratorRestoreTarget's read-and-clear contract for "what
// happened since I last ran".
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

// ConsumeInterruptedActivityNotice reports the work a previous launch's
// confirmed close interrupted, if any, and clears the record so it surfaces
// only once. The frontend calls this during boot, directly after
// LoadState, and shows it as a notification rather than starting blank.
func (a *App) ConsumeInterruptedActivityNotice() []activityQueueEntry {
	record, ok := consumeInterruptedActivityRecord(a.deps.interruptedActivityPath)
	if !ok {
		return nil
	}
	return record.Entries
}
