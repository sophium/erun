package jobs

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared/jobexclusive"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

func TestStartTaskEnvironmentJobRecordsATypedResult(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	type taskResult struct {
		Value string `json:"value"`
	}
	// Block the work so the handle is read while the job is still running.
	done := make(chan struct{})
	release := make(chan struct{})
	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "test-task",
		Run: func(io.Writer) (any, error) {
			defer close(done)
			<-release
			return taskResult{Value: "ok"}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	if job.State != EnvironmentJobStateRunning {
		t.Fatalf("job started in state %q, want running", job.State)
	}
	if job.Kind != EnvironmentJobKindTask {
		t.Fatalf("job kind = %q, want %q", job.Kind, EnvironmentJobKindTask)
	}
	close(release)
	<-done
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	finished, err := LoadEnvironmentJob(tenant, environment, job.ID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !finished.Succeeded {
		t.Fatalf("finished job = %+v, want succeeded", finished)
	}
	var decoded taskResult
	if err := json.Unmarshal(finished.Result, &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if decoded.Value != "ok" {
		t.Fatalf("decoded result = %+v, want value=ok", decoded)
	}
}

func TestStartTaskEnvironmentJobRecordsAFailure(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "failing-task",
		Run: func(io.Writer) (any, error) {
			return nil, errors.New("boom")
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	finished, err := LoadEnvironmentJob(tenant, environment, job.ID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if finished.Succeeded {
		t.Fatalf("finished job = %+v, want failed", finished)
	}
	if finished.ExitCode == nil || *finished.ExitCode == 0 {
		t.Fatalf("exit code = %v, want non-zero", finished.ExitCode)
	}
	if finished.Reason != "boom" {
		t.Fatalf("reason = %q, want %q", finished.Reason, "boom")
	}
}

func TestStartTaskEnvironmentJobRecoversAPanic(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "panicking-task",
		Run: func(io.Writer) (any, error) {
			panic("kaboom")
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	finished, err := LoadEnvironmentJob(tenant, environment, job.ID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if finished.State != jobstate.EnvironmentJobStateExited {
		t.Fatalf("state = %q, want exited (never left running)", finished.State)
	}
	if finished.Succeeded {
		t.Fatalf("a panicking task must never read as succeeded")
	}
}

func TestStartTaskEnvironmentJobRefusesAnIDStillRunning(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	release := make(chan struct{})
	started := make(chan struct{})
	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "blocking-task",
		ID:          "same-id",
		Run: func(io.Writer) (any, error) {
			close(started)
			<-release
			return struct{}{}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	<-started
	defer close(release)

	if _, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "blocking-task-again",
		ID:          job.ID,
		Run: func(io.Writer) (any, error) {
			return struct{}{}, nil
		},
	}); err == nil {
		t.Fatal("starting a second task job with an id still running must be refused")
	}
}

// waitForEnvironmentJobFinished polls, bounded, until the job records a terminal outcome.
func waitForEnvironmentJobFinished(t *testing.T, tenant, environment, id string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := LoadEnvironmentJob(tenant, environment, id, time.Now())
		if err != nil {
			t.Fatalf("LoadEnvironmentJob: %v", err)
		}
		if job.Finished() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %q did not finish within the deadline", id)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelEnvironmentJobRefusesATaskJob(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	release := make(chan struct{})
	started := make(chan struct{})
	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "cancel-me",
		Run: func(io.Writer) (any, error) {
			close(started)
			<-release
			return struct{}{}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	<-started
	defer close(release)

	if _, err := CancelEnvironmentJob(core.Context{}, model.CancelEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          job.ID,
	}); err == nil {
		t.Fatal("cancelling a task job must be refused: its PID is this process's own, not a subprocess to signal")
	}
}

// A task job has no captured stdio, so the log it is handed is its only output.
func TestStartTaskEnvironmentJobServesWhatTheWorkWrote(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "logging-task",
		Run: func(log io.Writer) (any, error) {
			_, _ = io.WriteString(log, "docker build failed: no space left on device\n")
			return nil, errors.New("exit status 1")
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	output, err := ReadEnvironmentJobOutput(model.ReadEnvironmentJobOutputParams{Tenant: tenant, Environment: environment, ID: job.ID})
	if err != nil {
		t.Fatalf("ReadEnvironmentJobOutput: %v", err)
	}
	if !strings.Contains(output.Output, "no space left on device") {
		t.Fatalf("job output = %q, want the work's own output; a failed task with no log is undiagnosable", output.Output)
	}
	if output.Job.OutputBytes == 0 {
		t.Fatalf("outputBytes = 0 with a non-empty log; a reader is told there is nothing to read")
	}
	if _, err := os.Stat(output.Job.LogPath); err != nil {
		t.Fatalf("stat log path %q: %v", output.Job.LogPath, err)
	}
}

// The log honours the command-job cap and records truncation.
func TestStartTaskEnvironmentJobCapsItsLog(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:         tenant,
		Environment:    environment,
		Name:           "chatty-task",
		MaxOutputBytes: 16,
		Run: func(log io.Writer) (any, error) {
			_, _ = io.WriteString(log, strings.Repeat("x", 1024))
			return struct{}{}, nil
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	finished, err := LoadEnvironmentJob(tenant, environment, job.ID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if finished.OutputBytes != 16 {
		t.Fatalf("outputBytes = %d, want the 16-byte cap", finished.OutputBytes)
	}
	if !finished.OutputTruncated {
		t.Fatalf("finished job = %+v, want outputTruncated: a capped log must never read as the whole story", finished)
	}
}

// A parent must not report its own exit while a task it started still runs.
func TestATaskJobIsFoundByTheJobThatStartedIt(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, err := jobexclusive.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{})
	child, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:         tenant,
		Environment:    environment,
		Name:           "build",
		StartedByJobID: "parent-job",
		Run: func(io.Writer) (any, error) {
			close(started)
			<-release
			return nil, errors.New("exit status 1")
		},
	})
	if err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	<-started

	if child.StartedByJobID != "parent-job" {
		t.Fatalf("startedByJobId = %q, want parent-job", child.StartedByJobID)
	}
	running := environmentJobRunningChildren(dir, "parent-job", time.Now())
	if len(running) != 1 || running[0].ID != child.ID {
		t.Fatalf("running children = %+v, want the task job %q", running, child.ID)
	}

	close(release)
	waitForEnvironmentJobFinished(t, tenant, environment, child.ID)

	failed := environmentJobFailedChildren(dir, "parent-job", time.Now())
	if len(failed) != 1 || failed[0].ID != child.ID {
		t.Fatalf("failed children = %+v, want the failed task job %q", failed, child.ID)
	}
}

// Handoff work must not make its parent wait.
func TestAHandoffTaskJobIsExcludedFromItsParentsFinishCheck(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir, err := jobexclusive.EnvironmentJobDir(tenant, environment)
	if err != nil {
		t.Fatalf("environmentJobDir: %v", err)
	}

	release := make(chan struct{})
	started := make(chan struct{})
	if _, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:         tenant,
		Environment:    environment,
		Name:           "release",
		StartedByJobID: "parent-job",
		Handoff:        true,
		Run: func(io.Writer) (any, error) {
			close(started)
			<-release
			return struct{}{}, nil
		},
	}); err != nil {
		t.Fatalf("StartTaskEnvironmentJob: %v", err)
	}
	<-started

	if running := environmentJobRunningChildren(dir, "parent-job", time.Now()); len(running) != 0 {
		t.Fatalf("running children = %+v, want none: a handoff task must never hold its parent's finish check", running)
	}

	// Wait for the goroutine's cleanup so it cannot race the next test's XDG isolation.
	close(release)
	waitForEnvironmentJobFinished(t, tenant, environment, "release")
}
