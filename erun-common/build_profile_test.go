package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// withTimingHome points timingRecordDir at a fresh temp dir for the duration
// of the test by overriding HOME, since timingRecordDir derives from
// os.UserHomeDir with no injectable override.
func withTimingHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir also checks this on Windows
	return home
}

func writeFakeTimingRecord(t *testing.T, dir, command string, startedAt time.Time, failed bool) string {
	t.Helper()
	record := TimingRecord{
		Command:         command,
		StartedAt:       startedAt,
		DurationSeconds: 12.5,
		Duration:        "12.5s",
		Failed:          failed,
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal fixture record: %v", err)
	}
	name := timingRecordFileName(command, startedAt)
	if err := os.WriteFile(filepath.Join(dir, name), encoded, 0o600); err != nil {
		t.Fatalf("write fixture record: %v", err)
	}
	return name
}

func TestListTimingRecordsReturnsNewestFirst(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeFakeTimingRecord(t, dir, "build", base, false)
	writeFakeTimingRecord(t, dir, "build", base.Add(time.Hour), false)
	writeFakeTimingRecord(t, dir, "build", base.Add(2*time.Hour), true)
	// A different command must not appear in "build"'s listing.
	writeFakeTimingRecord(t, dir, "push", base.Add(3*time.Hour), false)

	summaries, err := ListTimingRecords("build", 0)
	if err != nil {
		t.Fatalf("ListTimingRecords: %v", err)
	}
	if len(summaries) != 3 {
		t.Fatalf("expected 3 build records, got %d: %+v", len(summaries), summaries)
	}
	if !summaries[0].Failed {
		t.Errorf("expected the newest (failed) record first, got %+v", summaries[0])
	}
	for i := 1; i < len(summaries); i++ {
		if summaries[i-1].ID < summaries[i].ID {
			t.Fatalf("expected newest-first ordering, got %+v", summaries)
		}
	}
}

func TestListTimingRecordsRespectsLimit(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		writeFakeTimingRecord(t, dir, "build", base.Add(time.Duration(i)*time.Hour), false)
	}
	summaries, err := ListTimingRecords("build", 2)
	if err != nil {
		t.Fatalf("ListTimingRecords: %v", err)
	}
	if len(summaries) != 2 {
		t.Fatalf("expected limit=2 to cap the result, got %d", len(summaries))
	}
}

func TestListTimingRecordsNoDirectoryReturnsEmptyNotError(t *testing.T) {
	withTimingHome(t)
	summaries, err := ListTimingRecords("build", 0)
	if err != nil {
		t.Fatalf("expected no error for a never-created timing dir, got %v", err)
	}
	if len(summaries) != 0 {
		t.Fatalf("expected no records, got %+v", summaries)
	}
}

func TestListTimingRecordsSkipsCorruptFile(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeFakeTimingRecord(t, dir, "build", base, false)
	corruptName := timingRecordFileName("build", base.Add(time.Hour))
	if err := os.WriteFile(filepath.Join(dir, corruptName), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt record: %v", err)
	}
	summaries, err := ListTimingRecords("build", 0)
	if err != nil {
		t.Fatalf("expected a corrupt record to be skipped, not error out: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("expected only the one valid record, got %+v", summaries)
	}
}

func TestLoadTimingRecordResolvesLatest(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	writeFakeTimingRecord(t, dir, "build", base, false)
	writeFakeTimingRecord(t, dir, "build", base.Add(time.Hour), true)

	record, err := LoadTimingRecord("build", "latest")
	if err != nil {
		t.Fatalf("LoadTimingRecord: %v", err)
	}
	if !record.Failed {
		t.Errorf("expected \"latest\" to resolve to the newest (failed) record")
	}
}

func TestLoadTimingRecordByExplicitID(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	name := writeFakeTimingRecord(t, dir, "build", base, false)
	id := name[:len(name)-len(".json")]

	record, err := LoadTimingRecord("build", id)
	if err != nil {
		t.Fatalf("LoadTimingRecord: %v", err)
	}
	if record.Command != "build" {
		t.Errorf("expected the build record, got %+v", record)
	}

	// Also resolves with the .json suffix already attached.
	if _, err := LoadTimingRecord("build", name); err != nil {
		t.Fatalf("LoadTimingRecord with .json suffix: %v", err)
	}
}

func TestLoadTimingRecordNoRecordsIsAClearError(t *testing.T) {
	withTimingHome(t)
	if _, err := LoadTimingRecord("build", "latest"); err == nil {
		t.Fatalf("expected an error when no build records exist")
	}
}

func TestLoadTimingRecordUnknownIDIsAClearError(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFakeTimingRecord(t, dir, "build", time.Now(), false)
	if _, err := LoadTimingRecord("build", "build-does-not-exist"); err == nil {
		t.Fatalf("expected an error for an unknown record id")
	}
}

func TestWriteTimingRecordPrunesOldestBeyondRetentionLimit(t *testing.T) {
	withTimingHome(t)
	dir, err := timingRecordDir()
	if err != nil {
		t.Fatalf("timingRecordDir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < maxTimingRecordsRetained+5; i++ {
		writeFakeTimingRecord(t, dir, "build", base.Add(time.Duration(i)*time.Minute), false)
	}
	pruneTimingRecords(dir, "build")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	names := timingRecordFileNamesForCommand(entries, "build")
	if len(names) != maxTimingRecordsRetained {
		t.Fatalf("expected pruning to cap at %d records, got %d", maxTimingRecordsRetained, len(names))
	}
}

func TestRenderTimingRecordRowsSortsByDurationAndShowsCgroup(t *testing.T) {
	record := TimingRecord{
		Command:  "build",
		Duration: "10s",
		Steps: []TimingStepJSON{
			{Name: "fast-image", Duration: "1s", DurationSeconds: 1},
			{
				Name: "slow-image", Duration: "9s", DurationSeconds: 9,
				Cgroup: &BuildCgroupMetrics{Available: true, CPUSeconds: 8.5, ThrottledPeriods: 3, TotalPeriods: 10},
			},
		},
	}
	rows := RenderTimingRecordRows(record)
	if len(rows) != 3 {
		t.Fatalf("expected root + 2 step rows, got %+v", rows)
	}
	got := rows[1]
	for _, want := range []string{"slow-image", "cpu=8.5s", "throttled 3/10 periods"} {
		if !strings.Contains(got, want) {
			t.Errorf("expected row %q to contain %q", got, want)
		}
	}
}
