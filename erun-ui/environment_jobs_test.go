package main

import (
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// A missing outcome must never be readable as a successful zero, and an agent
// that has not emitted must never render a made-up progress row.
func TestJobToUIKeepsAbsentOutcomesAbsent(t *testing.T) {
	running := environmentJobToUI(eruncommon.EnvironmentJob{
		ID: "j1", Name: "build", State: "running", Kind: "command",
		StartedAt: time.Unix(1700000000, 0),
	})
	if running.ExitCode != nil {
		t.Fatalf("a running job has no exit code, got %v", *running.ExitCode)
	}
	if running.Progress != nil {
		t.Fatal("a command job has no agent progress")
	}
	if running.EndedAtUnix != 0 {
		t.Fatalf("a running job has not ended, got %d", running.EndedAtUnix)
	}
	if running.StartedAtUnix != 1700000000 {
		t.Fatalf("start time: got %d", running.StartedAtUnix)
	}

	code := 0
	exited := environmentJobToUI(eruncommon.EnvironmentJob{
		ID: "j2", State: "exited", ExitCode: &code,
		StartedAt: time.Unix(1700000000, 0), EndedAt: time.Unix(1700000042, 0),
	})
	if exited.ExitCode == nil || *exited.ExitCode != 0 {
		t.Fatalf("a real zero exit must survive, got %v", exited.ExitCode)
	}
	if exited.EndedAtUnix != 1700000042 {
		t.Fatalf("end time: got %d", exited.EndedAtUnix)
	}
}

func TestJobReadsRequireTheirIdentifiers(t *testing.T) {
	app := &App{}
	if _, err := app.LoadEnvironmentJobs(" ", "build"); err == nil ||
		!strings.Contains(err.Error(), "tenant and environment are required") {
		t.Fatalf("blank tenant: %v", err)
	}
	if _, err := app.ReadEnvironmentJobOutput(uiJobOutputInput{Tenant: "t", Environment: "e"}); err == nil ||
		!strings.Contains(err.Error(), "job id are required") {
		t.Fatalf("blank job id: %v", err)
	}
	if _, err := app.CancelEnvironmentJob(uiCancelJobInput{Tenant: "t", Environment: "e"}); err == nil {
		t.Fatal("cancel without a job id must be refused")
	}
}
