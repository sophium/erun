package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConsumeInterruptedActivityNoticeReadsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted-activity.json")
	app := NewApp(erunUIDeps{interruptedActivityPath: path})

	if err := writeInterruptedActivityRecord(path, []activityQueueEntry{
		{ID: "release-1", Command: "release", Tenant: "acme", Environment: "prod"},
	}); err != nil {
		t.Fatalf("writeInterruptedActivityRecord failed: %v", err)
	}

	entries := app.ConsumeInterruptedActivityNotice()
	if len(entries) != 1 || entries[0].ID != "release-1" {
		t.Fatalf("unexpected entries: %+v", entries)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected the record to be consumed (deleted), stat err=%v", err)
	}

	// A second call after the first consumed the record must report nothing,
	// so a repeated boot-time call never re-shows a stale notice.
	if entries := app.ConsumeInterruptedActivityNotice(); entries != nil {
		t.Fatalf("expected nil on the second read, got %+v", entries)
	}
}

func TestConsumeInterruptedActivityNoticeNoFile(t *testing.T) {
	app := NewApp(erunUIDeps{interruptedActivityPath: filepath.Join(t.TempDir(), "missing.json")})
	if entries := app.ConsumeInterruptedActivityNotice(); entries != nil {
		t.Fatalf("expected nil when no record exists, got %+v", entries)
	}
}

func TestWriteInterruptedActivityRecordNoopWhenEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "interrupted-activity.json")
	if err := writeInterruptedActivityRecord(path, nil); err != nil {
		t.Fatalf("writeInterruptedActivityRecord failed: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected no file to be written for an empty entry list, stat err=%v", err)
	}
}
