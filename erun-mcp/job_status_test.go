package erunmcp

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// exec_job_status and exec_job_await both echo back a job's command, and for
// an agent job that command embeds the entire dispatch prompt (#1561): a
// caller polling a lane, or re-awaiting one that keeps timing out, pays for
// that prompt's tokens on every single call for a field it supplied itself.
// These tests pin the two size/shape properties that fix relies on: the
// command a status/await response carries never grows past the preview bound,
// and asking for one job never smuggles a second copy of it into the list.

// writeJobFixture plants a job record directly on disk in the shape
// LoadEnvironmentJob/LoadEnvironmentJobs read back, without needing a real
// supervisor process behind it -- job.State exited short-circuits the
// liveness reconciliation that only applies to a job still claiming running.
func writeJobFixture(t *testing.T, tenant, environment string, job eruncommon.EnvironmentJob) {
	t.Helper()
	dir, err := eruncommon.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		t.Fatalf("EnvironmentActivityDir: %v", err)
	}
	jobsDir := filepath.Join(dir, "jobs")
	if err := os.MkdirAll(jobsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	data, err := json.Marshal(job)
	if err != nil {
		t.Fatalf("Marshal job fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(jobsDir, job.ID+".json"), data, 0o644); err != nil {
		t.Fatalf("WriteFile job fixture: %v", err)
	}
}

func finishedJobFixture(id string, command []string) eruncommon.EnvironmentJob {
	zero := 0
	now := time.Now()
	return eruncommon.EnvironmentJob{
		ID:        id,
		Name:      id,
		State:     eruncommon.EnvironmentJobStateExited,
		Command:   command,
		ExitCode:  &zero,
		StartedAt: now.Add(-time.Minute),
		EndedAt:   now,
	}
}

func TestJobStatusCollapsesALongCommandByDefault(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	// Shaped like an agent job's argv: a tool invocation carrying a multi-KB
	// prompt as one of its arguments.
	prompt := strings.Repeat("investigate and fix the failing lane ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep", []string{"claude", "-p", prompt}))

	_, result, err := jobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "sweep"})
	if err != nil {
		t.Fatalf("job_status: %v", err)
	}
	if result.Job == nil {
		t.Fatalf("job_status returned no job for id %q", "sweep")
	}
	got := strings.Join(result.Job.Command, " ")
	if len(got) >= len(prompt) {
		t.Fatalf("command was not collapsed: got %d chars, original prompt alone was %d", len(got), len(prompt))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("collapsed command %q does not end in the truncation marker", got)
	}
}

func TestJobStatusLeavesAShortCommandUntouched(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("suite", []string{"./gradlew", "test"}))

	_, result, err := jobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "suite"})
	if err != nil {
		t.Fatalf("job_status: %v", err)
	}
	if result.Job == nil {
		t.Fatalf("job_status returned no job for id %q", "suite")
	}
	want := []string{"./gradlew", "test"}
	if len(result.Job.Command) != len(want) || result.Job.Command[0] != want[0] || result.Job.Command[1] != want[1] {
		t.Fatalf("short command was altered: got %v, want %v", result.Job.Command, want)
	}
}

func TestJobStatusByIDDoesNotDuplicateTheJob(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("suite", []string{"./gradlew", "test"}))

	_, result, err := jobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "suite"})
	if err != nil {
		t.Fatalf("job_status: %v", err)
	}
	if result.Job == nil {
		t.Fatalf("job_status returned no job for id %q", "suite")
	}
	if len(result.Jobs) != 0 {
		t.Fatalf("expected jobs to stay empty when id selected one job, got %d entries", len(result.Jobs))
	}
}

func TestJobStatusListsCollapseEveryJobsCommand(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	prompt := strings.Repeat("build the release image and push it ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep-a", []string{"claude", "-p", prompt}))
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep-b", []string{"codex", "exec", prompt}))

	_, result, err := jobStatusTool(runtime)(context.Background(), nil, JobStatusInput{})
	if err != nil {
		t.Fatalf("job_status: %v", err)
	}
	if len(result.Jobs) != 2 {
		t.Fatalf("expected both fixtures listed, got %d", len(result.Jobs))
	}
	for _, job := range result.Jobs {
		got := strings.Join(job.Command, " ")
		if len(got) >= len(prompt) {
			t.Fatalf("job %s command was not collapsed: got %d chars", job.ID, len(got))
		}
	}
}

func TestJobAwaitCollapsesALongCommand(t *testing.T) {
	isolateLeaseCache(t)
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	prompt := strings.Repeat("investigate and fix the failing lane ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep", []string{"claude", "-p", prompt}))

	// The job is already finished, so await returns on its first read rather
	// than actually waiting out the timeout.
	_, result, err := jobAwaitTool(runtime)(context.Background(), nil, JobAwaitInput{ID: "sweep", TimeoutSeconds: 5})
	if err != nil {
		t.Fatalf("job_await: %v", err)
	}
	got := strings.Join(result.Job.Command, " ")
	if len(got) >= len(prompt) {
		t.Fatalf("command was not collapsed: got %d chars, original prompt alone was %d", len(got), len(prompt))
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("collapsed command %q does not end in the truncation marker", got)
	}
}

func TestPreviewCommandBoundary(t *testing.T) {
	atBound := strings.Repeat("a", jobCommandPreviewChars)
	if _, truncated := previewCommand([]string{atBound}); truncated {
		t.Fatalf("a command exactly at the bound must not be reported as truncated")
	}
	overBound := strings.Repeat("a", jobCommandPreviewChars+1)
	preview, truncated := previewCommand([]string{overBound})
	if !truncated {
		t.Fatalf("a command one rune over the bound must be reported as truncated")
	}
	if len(preview) != 1 {
		t.Fatalf("expected a single collapsed element, got %v", preview)
	}
	if !strings.HasSuffix(preview[0], "…") {
		t.Fatalf("collapsed command %q does not end in the truncation marker", preview[0])
	}
}
