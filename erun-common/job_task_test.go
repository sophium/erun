package eruncommon

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestStartTaskEnvironmentJobRecordsATypedResult(t *testing.T) {
	tenant, environment := "acme", "dev"
	t.Setenv("XDG_CACHE_HOME", t.TempDir())

	type taskResult struct {
		Value string `json:"value"`
	}
	done := make(chan struct{})
	job, err := StartTaskEnvironmentJob(TaskEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		Name:        "test-task",
		Run: func() (any, error) {
			defer close(done)
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

	<-done
	waitForEnvironmentJobFinished(t, tenant, environment, job.ID)

	finished, err := LoadEnvironmentJob(tenant, environment, job.ID, time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if !finished.Succeeded() {
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
		Run: func() (any, error) {
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
	if finished.Succeeded() {
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
		Run: func() (any, error) {
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
	if finished.State != EnvironmentJobStateExited {
		t.Fatalf("state = %q, want exited (never left running)", finished.State)
	}
	if finished.Succeeded() {
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
		Run: func() (any, error) {
			close(started)
			<-release
			return nil, nil
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
		Run: func() (any, error) {
			return nil, nil
		},
	}); err == nil {
		t.Fatal("starting a second task job with an id still running must be refused")
	}
}

// waitForEnvironmentJobFinished polls the store until the job's goroutine has
// recorded a terminal outcome, bounded so a bug that leaves it running forever
// fails the test instead of hanging the suite.
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
