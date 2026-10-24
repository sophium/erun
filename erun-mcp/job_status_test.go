package erunmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// exec_job_status and exec_job_await both echo back a job's command, and for
// an agent job that command embeds the entire dispatch prompt: a caller
// polling a lane, or re-awaiting one that keeps timing out, pays for that
// prompt's tokens on every single call for a field it supplied itself. These
// tests pin the two size/shape properties the fix relies on: the command a
// status/await response carries never grows past the preview bound, and
// asking for one job never smuggles a second copy of it into the list.

// writeJobFixture plants a job record directly on disk in the shape
// LoadEnvironmentJob/LoadEnvironmentJobs read back, without needing a real
// supervisor process behind it -- job.State exited short-circuits the
// liveness reconciliation that only applies to a job still claiming running.
func writeJobFixture(t *testing.T, tenant, environment string, job shared.EnvironmentJob) {
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

func finishedJobFixture(id string, command []string) shared.EnvironmentJob {
	zero := 0
	now := time.Now()
	return shared.EnvironmentJob{
		ID:        id,
		Name:      id,
		State:     shared.EnvironmentJobStateExited,
		Command:   command,
		ExitCode:  &zero,
		StartedAt: now.Add(-time.Minute),
		EndedAt:   now,
	}
}
