package erunmcp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// An agent job's command embeds the whole prompt, so status/await must truncate it and
// never duplicate it.

// writeJobFixture writes an exited job, which skips liveness reconciliation.
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
