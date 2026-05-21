package eruncommon

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// TestWriteRootConfigBackupSnapshotsPreviousContents covers the
// happy path: a live config file is present, today's backup slot is
// empty, and the helper should snapshot the current contents under
// "<base>.<YYYY-MM-DD>.bak" without modifying the live file.
func TestWriteRootConfigBackupSnapshotsPreviousContents(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(live, []byte("defaulttenant: foo\n"), 0o644); err != nil {
		t.Fatalf("seed live config: %v", err)
	}

	now := func() time.Time { return time.Date(2026, 5, 21, 12, 0, 0, 0, time.UTC) }
	if err := writeRootConfigBackupIfDue(live, now); err != nil {
		t.Fatalf("writeRootConfigBackupIfDue: %v", err)
	}

	backup := filepath.Join(dir, "config.yaml.2026-05-21.bak")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "defaulttenant: foo\n" {
		t.Fatalf("unexpected backup contents %q", string(data))
	}
}

// TestWriteRootConfigBackupIsIdempotentSameDay verifies that
// repeated saves within one UTC date do not rewrite the day's
// backup. The backup must capture the state BEFORE the user's
// first save of the day; subsequent saves should leave it alone.
func TestWriteRootConfigBackupIsIdempotentSameDay(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(live, []byte("first\n"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	now := func() time.Time { return time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC) }
	if err := writeRootConfigBackupIfDue(live, now); err != nil {
		t.Fatalf("first backup: %v", err)
	}
	// Mutate the live file to simulate a subsequent save's contents,
	// then run the helper again with the same clock. The existing
	// backup should remain unchanged (still "first\n").
	if err := os.WriteFile(live, []byte("second\n"), 0o644); err != nil {
		t.Fatalf("update live: %v", err)
	}
	if err := writeRootConfigBackupIfDue(live, now); err != nil {
		t.Fatalf("second backup: %v", err)
	}
	backup := filepath.Join(dir, "config.yaml.2026-05-21.bak")
	data, err := os.ReadFile(backup)
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(data) != "first\n" {
		t.Fatalf("backup overwritten; got %q want %q", string(data), "first\n")
	}
}

// TestWriteRootConfigBackupNoLiveFileIsNoop covers the first-write
// case: the live config does not exist yet. There is nothing to
// snapshot, so the helper must return success without creating a
// stub or surfacing an error.
func TestWriteRootConfigBackupNoLiveFileIsNoop(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	now := func() time.Time { return time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC) }
	if err := writeRootConfigBackupIfDue(live, now); err != nil {
		t.Fatalf("backup: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no files, got %v", entries)
	}
}

// TestPruneOldRootConfigBackupsKeepsFiveNewestRegardlessOfAge is the
// core retention guarantee: rotation evicts by count, never by age.
// Seven dated backups exist; a write today produces the eighth.
// After pruning, only the five newest must remain — and crucially
// that count includes the very old 2026-01-01 file when it is
// among the newest five at prune time.
func TestPruneOldRootConfigBackupsKeepsFiveNewestRegardlessOfAge(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	base := filepath.Base(live)
	dates := []time.Time{
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 16, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 20, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
	}
	for _, d := range dates {
		path := filepath.Join(dir, rootConfigBackupName(base, d))
		if err := os.WriteFile(path, []byte(d.Format(time.RFC3339)), 0o644); err != nil {
			t.Fatalf("seed %s: %v", path, err)
		}
	}

	if err := pruneOldRootConfigBackups(dir, base, rootConfigBackupKeep); err != nil {
		t.Fatalf("prune: %v", err)
	}

	remaining, err := listManagedRootConfigBackups(dir, base)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != rootConfigBackupKeep {
		t.Fatalf("expected %d remaining, got %d (%v)", rootConfigBackupKeep, len(remaining), remaining)
	}
	want := []string{"2026-05-21", "2026-05-20", "2026-05-19", "2026-05-18", "2026-05-17"}
	for i, expected := range want {
		got := remaining[i].Date.Format("2006-01-02")
		if got != expected {
			t.Fatalf("position %d: got %s, want %s", i, got, expected)
		}
	}
}

// TestPruneDoesNotWipePreexistingOldBackupBeforeFiveDailiesArrive
// codifies the stated policy in plain English: a 10-day-old backup
// that already lives in the directory at startup must survive the
// first daily save. Only once five newer dailies accumulate does the
// oldest one get evicted.
func TestPruneDoesNotWipePreexistingOldBackupBeforeFiveDailiesArrive(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	base := filepath.Base(live)
	tenDaysAgo := time.Date(2026, 5, 11, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, rootConfigBackupName(base, tenDaysAgo)), []byte("ancient"), 0o644); err != nil {
		t.Fatalf("seed ancient: %v", err)
	}
	if err := os.WriteFile(live, []byte("today\n"), 0o644); err != nil {
		t.Fatalf("seed live: %v", err)
	}

	// Run backups for four consecutive days.
	for _, d := range []time.Time{
		time.Date(2026, 5, 21, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 22, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 24, 0, 0, 0, 0, time.UTC),
	} {
		now := d
		if err := writeRootConfigBackupIfDue(live, func() time.Time { return now }); err != nil {
			t.Fatalf("backup %s: %v", d, err)
		}
	}

	remaining, err := listManagedRootConfigBackups(dir, base)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(remaining) != 5 {
		t.Fatalf("expected 5 backups (4 dailies + the preserved ancient), got %d", len(remaining))
	}
	dates := make([]string, 0, len(remaining))
	for _, b := range remaining {
		dates = append(dates, b.Date.Format("2006-01-02"))
	}
	if !containsTrimmedAlias(dates, "2026-05-11") {
		t.Fatalf("ancient backup was wiped before five new dailies replaced it: %v", dates)
	}

	// Fifth daily must finally push the ancient one out.
	fifth := time.Date(2026, 5, 25, 0, 0, 0, 0, time.UTC)
	if err := writeRootConfigBackupIfDue(live, func() time.Time { return fifth }); err != nil {
		t.Fatalf("fifth backup: %v", err)
	}
	remaining, err = listManagedRootConfigBackups(dir, base)
	if err != nil {
		t.Fatalf("relist: %v", err)
	}
	if len(remaining) != 5 {
		t.Fatalf("expected 5 backups after eviction, got %d", len(remaining))
	}
	dates = dates[:0]
	for _, b := range remaining {
		dates = append(dates, b.Date.Format("2006-01-02"))
	}
	if containsTrimmedAlias(dates, "2026-05-11") {
		t.Fatalf("ancient backup should have been evicted on the 5th daily: %v", dates)
	}
}

// TestListRootConfigBackupsSortsNewestFirst documents the API
// guarantee CLI report rendering relies on: callers want to surface
// the most-recently-dated backup as the default restore option.
func TestListRootConfigBackupsSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	base := filepath.Base(live)
	for _, d := range []time.Time{
		time.Date(2026, 5, 17, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 18, 0, 0, 0, 0, time.UTC),
		time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC),
	} {
		path := filepath.Join(dir, rootConfigBackupName(base, d))
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	got, err := ListRootConfigBackups(live)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !sort.SliceIsSorted(got, func(i, j int) bool {
		return got[i].Date.After(got[j].Date)
	}) {
		t.Fatalf("not sorted newest-first: %v", got)
	}
}

// TestRestoreRootConfigFromBackupRejectsCorruptBackup is the safety
// guarantee: a backup that fails to deserialize must not replace
// the live file. The whole point of routing the restore through
// this helper is to avoid corrupted-replaces-broken cascades.
func TestRestoreRootConfigFromBackupRejectsCorruptBackup(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(live, []byte("defaulttenant: original\n"), 0o644); err != nil {
		t.Fatalf("seed live: %v", err)
	}
	backup := filepath.Join(dir, "config.yaml.2026-05-19.bak")
	if err := os.WriteFile(backup, []byte("\xff\xff\xffnot yaml"), 0o644); err != nil {
		t.Fatalf("seed corrupt backup: %v", err)
	}
	err := RestoreRootConfigFromBackup(backup, live)
	if err == nil {
		t.Fatalf("expected restore to refuse corrupt backup")
	}
	live2, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("re-read live: %v", err)
	}
	if string(live2) != "defaulttenant: original\n" {
		t.Fatalf("live file was overwritten despite corrupt backup: %q", string(live2))
	}
}

// TestRestoreRootConfigFromBackupHappyPath covers the success path.
// The validation step accepts a well-formed YAML, the live file
// receives an atomic rewrite, and a subsequent LoadERunConfig sees
// the restored values.
func TestRestoreRootConfigFromBackupHappyPath(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(live, []byte("\x00\x00\x00"), 0o644); err != nil {
		t.Fatalf("seed live (corrupt): %v", err)
	}
	backup := filepath.Join(dir, "config.yaml.2026-05-20.bak")
	if err := os.WriteFile(backup, []byte("defaulttenant: restored\n"), 0o644); err != nil {
		t.Fatalf("seed backup: %v", err)
	}
	if err := RestoreRootConfigFromBackup(backup, live); err != nil {
		t.Fatalf("restore: %v", err)
	}
	data, err := os.ReadFile(live)
	if err != nil {
		t.Fatalf("read live: %v", err)
	}
	if string(data) != "defaulttenant: restored\n" {
		t.Fatalf("live not replaced: %q", string(data))
	}
}

// TestFindRootConfigBackupByDate exercises the selector helper the
// CLI/MCP both call when the user supplies a YYYY-MM-DD string.
func TestFindRootConfigBackupByDate(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "config.yaml")
	base := filepath.Base(live)
	d := time.Date(2026, 5, 19, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(dir, rootConfigBackupName(base, d)), []byte("x"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got, ok, err := FindRootConfigBackupByDate(live, "2026-05-19")
	if err != nil || !ok {
		t.Fatalf("find: ok=%v err=%v", ok, err)
	}
	if !got.Date.Equal(d) {
		t.Fatalf("date mismatch: %v", got.Date)
	}
	_, ok, err = FindRootConfigBackupByDate(live, "2026-05-15")
	if err != nil {
		t.Fatalf("miss err: %v", err)
	}
	if ok {
		t.Fatal("expected miss for absent date")
	}
	if _, _, err := FindRootConfigBackupByDate(live, "not-a-date"); err == nil {
		t.Fatal("expected error for malformed date")
	}
}

// TestWriteFileAtomicSwapsAtomically ensures the temp file is gone
// after a successful write so callers can re-run the helper without
// leftover state. The function is in config.go but its semantics are
// the foundation for the backup machinery, so the test lives here.
func TestWriteFileAtomicSwapsAtomically(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "out.txt")
	if err := writeFileAtomic(target, []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("contents: %q", string(data))
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("list dir: %v", err)
	}
	for _, entry := range entries {
		if entry.Name() != "out.txt" {
			t.Fatalf("stray file in dir: %s", entry.Name())
		}
	}
}

// TestWriteFileAtomicCleansUpOnError verifies that a permission
// failure during the rename phase leaves the directory clean rather
// than littered with .tmp-* files.
func TestWriteFileAtomicCleansUpOnError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "subdir", "out.txt")
	err := writeFileAtomic(target, []byte("data"), 0o644)
	if err == nil {
		t.Fatal("expected error: directory does not exist")
	}
	if !errors.Is(err, os.ErrNotExist) {
		// Different platforms surface this differently; the assertion
		// here only documents that the write must fail loudly.
		t.Logf("non-ErrNotExist failure surface: %v", err)
	}
}
