package eruncommon

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func newTestRunningCommandContext() Context {
	return Context{
		Logger: NewLoggerWithWriters(0, io.Discard, io.Discard),
		Stdout: io.Discard,
		Stderr: io.Discard,
	}
}

func TestRegisterRunningCommandWritesAndReleasesMarker(t *testing.T) {
	dir := t.TempDir()
	ctx := newTestRunningCommandContext()
	deps := runningCommandDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4242 },
	}
	handle, err := registerRunningCommand(ctx, RunningCommand{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
		Release:     "team-devops",
		Namespace:   "team-dev",
	}, deps)
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if handle == nil || handle.Path() == "" {
		t.Fatal("expected non-empty handle")
	}
	if _, statErr := os.Stat(handle.Path()); statErr != nil {
		t.Fatalf("marker not written: %v", statErr)
	}
	handle.Release()
	if _, statErr := os.Stat(handle.Path()); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("marker not removed on Release: %v", statErr)
	}
}

func TestRegisterRunningCommandDryRunWritesNothing(t *testing.T) {
	dir := t.TempDir()
	ctx := newTestRunningCommandContext()
	ctx.DryRun = true
	deps := runningCommandDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 4242 },
	}
	handle, err := registerRunningCommand(ctx, RunningCommand{Command: "deploy", Tenant: "t", Environment: "e"}, deps)
	if err != nil {
		t.Fatalf("dry-run register: %v", err)
	}
	if handle != nil {
		t.Fatalf("dry-run produced handle: %+v", handle)
	}
	commandsDir := filepath.Join(dir, runningCommandDirName)
	if _, statErr := os.Stat(commandsDir); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("dry-run created directory: %v", statErr)
	}
}

func TestRegisterRunningCommandRequiresCommand(t *testing.T) {
	dir := t.TempDir()
	ctx := newTestRunningCommandContext()
	deps := runningCommandDeps{
		configDir: func() (string, error) { return dir, nil },
		now:       func() time.Time { return time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC) },
		pid:       func() int { return 1 },
	}
	_, err := registerRunningCommand(ctx, RunningCommand{Tenant: "t"}, deps)
	if err == nil {
		t.Fatal("expected error when command field empty")
	}
}

func TestListRunningCommandsReturnsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	ctx := newTestRunningCommandContext()
	depsAt := func(when time.Time, pid int) runningCommandDeps {
		return runningCommandDeps{
			configDir: func() (string, error) { return dir, nil },
			now:       func() time.Time { return when },
			pid:       func() int { return pid },
		}
	}
	older := time.Date(2026, 5, 10, 11, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	if _, err := registerRunningCommand(ctx, RunningCommand{Command: "build", Tenant: "t", Component: "erun-devops"}, depsAt(older, 100)); err != nil {
		t.Fatalf("register older: %v", err)
	}
	if _, err := registerRunningCommand(ctx, RunningCommand{Command: "deploy", Tenant: "t", Environment: "dev"}, depsAt(newer, 200)); err != nil {
		t.Fatalf("register newer: %v", err)
	}
	got, err := listRunningCommands(runningCommandDeps{configDir: func() (string, error) { return dir, nil }}.resolved())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2", len(got))
	}
	if got[0].Command != "deploy" {
		t.Fatalf("newest command = %q, want deploy", got[0].Command)
	}
	if got[1].Command != "build" {
		t.Fatalf("oldest command = %q, want build", got[1].Command)
	}
}

func TestListRunningCommandsMissingDirReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	got, err := listRunningCommands(runningCommandDeps{
		configDir: func() (string, error) { return dir, nil },
	}.resolved())
	if err != nil {
		t.Fatalf("list missing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len = %d, want 0", len(got))
	}
}
