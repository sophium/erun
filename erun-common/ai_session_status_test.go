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
