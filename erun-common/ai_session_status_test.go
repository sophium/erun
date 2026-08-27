package eruncommon

import (
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func newAISessionStatusTestDirs(t *testing.T) (socketDir, statusDir string) {
	t.Helper()
	socketDir, err := os.MkdirTemp("", "erun-sock")
	if err != nil {
		t.Fatalf("temp socket dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketDir) })
	statusDir = t.TempDir()
	return socketDir, statusDir
}

func createAISessionSocket(t *testing.T, dir, tenant, environment, id string) {
	t.Helper()
	name := sanitizeForFilename(tenant) + "-" + sanitizeForFilename(environment) + "-" + sanitizeForFilename(id) + ".dtach"
	listener, err := net.Listen("unix", filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("create socket %s: %v", name, err)
	}
	t.Cleanup(func() { _ = listener.Close() })
}

// TestResolveAISessionStatusesReportsUnknownWithNoSocket is the honesty gate:
// a session id nobody ever opened has nothing to say, so it must not appear at
// all rather than default to any specific state.
func TestResolveAISessionStatusesReportsUnknownWithNoSocket(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(statuses) != 0 {
		t.Fatalf("expected no statuses with no sockets, got %+v", statuses)
	}
}

// TestResolveAISessionStatusesDegradesToUnknownWithNoSelfReport is the "tool
// cannot report a structured state" case (root AGENTS.md's degrade-honestly
// requirement): a live session with no report file must read as unknown, never
// as busy or idle inferred from anything else.
func TestResolveAISessionStatusesDegradesToUnknownWithNoSelfReport(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "codex")
	if len(statuses) != 1 {
		t.Fatalf("expected exactly one status, got %+v", statuses)
	}
	got := statuses[0]
	if got.State != AISessionStateUnknown {
		t.Fatalf("expected unknown state for a tool with no self-report, got %q", got.State)
	}
	if got.Tool != "codex" {
		t.Fatalf("expected tool %q to pass through, got %q", "codex", got.Tool)
	}
	if !got.LastActivity.IsZero() {
		t.Fatalf("expected zero LastActivity for an unreported session, got %v", got.LastActivity)
	}
}

// TestResolveAISessionStatusesReportsAwaitingInput is the red-then-green case
// for the state a volume heuristic can never produce: a session silently
// waiting on the operator must not read as idle just because it stopped
// producing output.
func TestResolveAISessionStatusesReportsAwaitingInput(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")

	// Red: before any report exists, the session is unknown, not idle — an
	// output-volume heuristic would have already called this idle after 3s of
	// silence, which is exactly the false reading this model must not produce.
	before := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(before) != 1 || before[0].State != AISessionStateUnknown {
		t.Fatalf("expected unknown before any report, got %+v", before)
	}

	writeAISessionSelfReportForTest(t, statusDir, "team", "dev", "ai", AISessionStateAwaitingInput)

	// Green: once the tool reports awaiting-input, the model says exactly that —
	// not idle, not busy.
	after := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(after) != 1 {
		t.Fatalf("expected exactly one status, got %+v", after)
	}
	got := after[0]
	if got.State != AISessionStateAwaitingInput {
		t.Fatalf("expected awaiting-input, got %q", got.State)
	}
	if got.LastActivity.IsZero() {
		t.Fatalf("expected LastActivity to be set from the report")
	}
}

// TestResolveAISessionStatusesQuietButWorkingIsNotIdle is the other failure
// mode of the old heuristic: a session busy inside a single long tool call
// produces no new bytes for long stretches, and must not flip to idle just
// because time passed.
func TestResolveAISessionStatusesQuietButWorkingIsNotIdle(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")
	writeAISessionSelfReportAtForTest(t, statusDir, "team", "dev", "ai", AISessionStateBusy, time.Now().Add(-10*time.Minute))

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(statuses) != 1 || statuses[0].State != AISessionStateBusy {
		t.Fatalf("expected a stale-but-unsuperseded busy report to still read busy, got %+v", statuses)
	}
}

// TestResolveAISessionStatusesChattyButStuckIsNotBusy is the mirror failure
// mode: continuous output cadence must never substitute for the tool's own
// idle report. Without any self-report mechanism at all wired for a tool, a
// session that is actually stuck must read unknown, never busy.
func TestResolveAISessionStatusesChattyButStuckIsNotBusy(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "codex")
	if len(statuses) != 1 || statuses[0].State != AISessionStateUnknown {
		t.Fatalf("expected unknown for a tool that never self-reports, got %+v", statuses)
	}
}

func TestResolveAISessionStatusesExitOutcomeOverridesSelfReport(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")
	writeAISessionSelfReportForTest(t, statusDir, "team", "dev", "ai", AISessionStateBusy)
	writeAISessionExitReportForTest(t, statusDir, "team", "dev", "ai", AISessionOutcomeOOMKilled, 137)

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(statuses) != 1 {
		t.Fatalf("expected exactly one status, got %+v", statuses)
	}
	got := statuses[0]
	if got.State != AISessionStateIdle {
		t.Fatalf("expected idle once the process has exited, got %q", got.State)
	}
	if got.Outcome != AISessionOutcomeOOMKilled || got.ExitCode != 137 {
		t.Fatalf("expected oom-killed/137, got outcome=%q exitCode=%d", got.Outcome, got.ExitCode)
	}
}

func TestResolveAISessionStatusesMalformedReportIsUnknown(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")
	if err := os.WriteFile(aiSessionReportPathIn(statusDir, "team", "dev", "ai"), []byte("not json"), 0o600); err != nil {
		t.Fatalf("write malformed report: %v", err)
	}

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(statuses) != 1 || statuses[0].State != AISessionStateUnknown {
		t.Fatalf("expected unknown for a malformed report, got %+v", statuses)
	}
}

// TestAISessionStatusReportCommandWritesTheReportItPromises runs the generated
// shell command for real, the same way a Claude Code hook would, and confirms
// ResolveAISessionStatuses reads back exactly what it wrote.
func TestAISessionStatusReportCommandWritesTheReportItPromises(t *testing.T) {
	socketDir, statusDir := newAISessionStatusTestDirs(t)
	createAISessionSocket(t, socketDir, "team", "dev", "ai")

	command := aiSessionStatusReportCommandIn(statusDir, "team", "dev", "ai", AISessionStateAwaitingInput)
	if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
		t.Fatalf("run report command: %v\n%s", err, output)
	}

	statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
	if len(statuses) != 1 || statuses[0].State != AISessionStateAwaitingInput {
		t.Fatalf("expected the command's own report to round-trip, got %+v", statuses)
	}
}

func TestAISessionExitReportCommandDistinguishesOOMFromOrdinaryExit(t *testing.T) {
	cases := []struct {
		exitStatus      string
		expectedOutcome AISessionOutcome
	}{
		{exitStatus: "137", expectedOutcome: AISessionOutcomeOOMKilled},
		{exitStatus: "0", expectedOutcome: AISessionOutcomeExited},
		{exitStatus: "1", expectedOutcome: AISessionOutcomeExited},
	}
	for _, tc := range cases {
		socketDir, statusDir := newAISessionStatusTestDirs(t)
		createAISessionSocket(t, socketDir, "team", "dev", "ai")

		command := "ai_status=" + tc.exitStatus + "; " + aiSessionExitReportCommandIn(statusDir, "team", "dev", "ai")
		if output, err := exec.Command("/bin/sh", "-c", command).CombinedOutput(); err != nil {
			t.Fatalf("run exit report command for status %s: %v\n%s", tc.exitStatus, err, output)
		}

		statuses := resolveAISessionStatusesIn(socketDir, statusDir, "team", "dev", "claude")
		if len(statuses) != 1 || statuses[0].Outcome != tc.expectedOutcome {
			t.Fatalf("exit status %s: expected outcome %q, got %+v", tc.exitStatus, tc.expectedOutcome, statuses)
		}
	}
}

func writeAISessionSelfReportForTest(t *testing.T, dir, tenant, environment, id string, state AISessionState) {
	t.Helper()
	writeAISessionSelfReportAtForTest(t, dir, tenant, environment, id, state, time.Now())
}

func writeAISessionSelfReportAtForTest(t *testing.T, dir, tenant, environment, id string, state AISessionState, at time.Time) {
	t.Helper()
	report := aiSessionSelfReport{State: state, AtUnix: at.Unix()}
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshal self report: %v", err)
	}
	if err := os.WriteFile(aiSessionReportPathIn(dir, tenant, environment, id), data, 0o600); err != nil {
		t.Fatalf("write self report: %v", err)
	}
}

func writeAISessionExitReportForTest(t *testing.T, dir, tenant, environment, id string, outcome AISessionOutcome, exitCode int) {
	t.Helper()
	exit := aiSessionExitReport{Outcome: outcome, ExitCode: exitCode, AtUnix: time.Now().Unix()}
	data, err := json.Marshal(exit)
	if err != nil {
		t.Fatalf("marshal exit report: %v", err)
	}
	if err := os.WriteFile(aiSessionExitPathIn(dir, tenant, environment, id), data, 0o600); err != nil {
		t.Fatalf("write exit report: %v", err)
	}
}
