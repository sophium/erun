package main

import (
	"bytes"
	"context"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppLogPathIsUnderTheERunSupportDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	path, err := appLogPath()
	if err != nil {
		t.Fatalf("appLogPath: %v", err)
	}
	if filepath.Base(path) != appLogFileName || filepath.Base(filepath.Dir(path)) != "ERun" {
		t.Fatalf("expected .../ERun/%s, got %q", appLogFileName, path)
	}
}

// The bug: the desktop is launched via `open`, which gives it no
// controlling terminal, so a log.Printf that only reaches stderr is invisible
// the moment the process is not run from a shell. initDurableAppLog gives it
// a second, durable destination every existing log.Printf call already writes
// to via the default logger.
func TestInitDurableAppLogCapturesLogPrintfCalls(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	restoreLogOutputAfter(t)

	closeLog := initDurableAppLog()
	log.Printf("erun-app: a partial-wiring skip that must survive")
	closeLog()

	path, err := appLogPath()
	if err != nil {
		t.Fatalf("appLogPath: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read durable log: %v", err)
	}
	if !strings.Contains(string(data), "a partial-wiring skip that must survive") {
		t.Fatalf("expected the log line in the durable file, got:\n%s", data)
	}
}

// restoreLogOutputAfter points the default logger back at its previous output
// once the test ends, so this test cannot leak its file handle into the rest
// of the suite (log.SetOutput is process-global state).
func restoreLogOutputAfter(t *testing.T) {
	t.Helper()
	t.Cleanup(func() { log.SetOutput(os.Stderr) })
}

func TestLoadAppLogReportsNoLogCapturedYetWhenAbsent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	app := NewApp(erunUIDeps{})
	t.Cleanup(func() { app.shutdown(context.Background()) })

	result, err := app.LoadAppLog()
	if err != nil {
		t.Fatalf("LoadAppLog: %v", err)
	}
	if result.Available {
		t.Fatalf("expected Available=false with no log file, got %+v", result)
	}
	if result.Reason != "no log captured yet" {
		t.Fatalf("expected the honest empty reason, got %q", result.Reason)
	}
}

func TestLoadAppLogReturnsTheDurableLogTail(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)

	app := NewApp(erunUIDeps{})
	t.Cleanup(func() { app.shutdown(context.Background()) })

	restoreLogOutputAfter(t)
	closeLog := initDurableAppLog()
	log.Printf("erun-app: orchestrator erun-issues spawned session 42")
	closeLog()

	result, err := app.LoadAppLog()
	if err != nil {
		t.Fatalf("LoadAppLog: %v", err)
	}
	if !result.Available {
		t.Fatalf("expected Available=true once the log has content, got %+v", result)
	}
	if !strings.Contains(result.Content, "orchestrator erun-issues spawned session 42") {
		t.Fatalf("expected the log line in the read tail, got:\n%s", result.Content)
	}
	path, err := appLogPath()
	if err != nil {
		t.Fatalf("appLogPath: %v", err)
	}
	if result.Path != path {
		t.Fatalf("expected Path %q, got %q", path, result.Path)
	}
}

func TestBoundedLogFileRotatesPastTheCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bounded.log")

	file, err := newBoundedLogFile(path, 32)
	if err != nil {
		t.Fatalf("newBoundedLogFile: %v", err)
	}
	defer func() { _ = file.Close() }()

	first := bytes.Repeat([]byte("a"), 20)
	if _, err := file.Write(first); err != nil {
		t.Fatalf("write first: %v", err)
	}
	second := bytes.Repeat([]byte("b"), 20)
	if _, err := file.Write(second); err != nil {
		t.Fatalf("write second: %v", err)
	}

	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read current: %v", err)
	}
	if string(current) != string(second) {
		t.Fatalf("expected the current file to hold only the write that triggered rotation, got %q", current)
	}
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("read backup: %v", err)
	}
	if string(backup) != string(first) {
		t.Fatalf("expected the backup to hold what was there before rotation, got %q", backup)
	}
}

func TestBoundedLogFilePicksUpExistingSizeOnOpen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bounded.log")
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), 25), 0o600); err != nil {
		t.Fatalf("seed existing file: %v", err)
	}

	file, err := newBoundedLogFile(path, 32)
	if err != nil {
		t.Fatalf("newBoundedLogFile: %v", err)
	}
	defer func() { _ = file.Close() }()

	// 25 already on disk + 10 more crosses the 32-byte cap, so this write must
	// rotate rather than silently growing the file past its bound.
	if _, err := file.Write(bytes.Repeat([]byte("y"), 10)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected a rotation given the pre-existing size, stat err: %v", err)
	}
}
