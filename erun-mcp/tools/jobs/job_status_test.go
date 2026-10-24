package jobs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// An agent job's command embeds its whole prompt, so status/await must bound
// it and never echo it twice.

// writeJobFixture writes an exited job, which skips supervisor liveness checks.
func writeJobFixture(t *testing.T, tenant, environment string, job jobstate.EnvironmentJob) {
	t.Helper()
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
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

func finishedJobFixture(id string, command []string) jobstate.EnvironmentJob {
	zero := 0
	now := time.Now()
	return jobstate.EnvironmentJob{
		ID:        id,
		Name:      id,
		State:     jobstate.EnvironmentJobStateExited,
		Command:   command,
		ExitCode:  &zero,
		StartedAt: now.Add(-time.Minute),
		EndedAt:   now,
	}
}

func TestJobStatusCollapsesALongCommandByDefault(t *testing.T) {
	isolateLeaseCache(t)
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	prompt := strings.Repeat("investigate and fix the failing lane ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep", []string{"claude", "-p", prompt}))

	_, result, err := JobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "sweep"})
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
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("suite", []string{"./gradlew", "test"}))

	_, result, err := JobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "suite"})
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
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("suite", []string{"./gradlew", "test"}))

	_, result, err := JobStatusTool(runtime)(context.Background(), nil, JobStatusInput{ID: "suite"})
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
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	prompt := strings.Repeat("build the release image and push it ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep-a", []string{"claude", "-p", prompt}))
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep-b", []string{"codex", "exec", prompt}))

	_, result, err := JobStatusTool(runtime)(context.Background(), nil, JobStatusInput{})
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
	runtime := runtimecfg.RuntimeConfig{Context: toolio.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}

	prompt := strings.Repeat("investigate and fix the failing lane ", 200)
	writeJobFixture(t, "tenant-a", "dev", finishedJobFixture("sweep", []string{"claude", "-p", prompt}))

	// The job has exited, so await returns on its first read.
	_, result, err := JobAwaitTool(runtime)(context.Background(), nil, JobAwaitInput{ID: "sweep", TimeoutSeconds: 5})
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
