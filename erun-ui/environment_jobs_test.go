package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
	eruncommon "github.com/sophium/erun/erun-common"
)

// isolateJobsCache points the job store at a temp dir for one test, so a test
// job never lands in the operator's real activity cache. t.Setenv alone is
// not enough: the xdg package resolves XDG_CACHE_HOME once at package init,
// before any test runs, so a later change needs an explicit Reload.
func isolateJobsCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

// jobsTestStore resolves one tenant/env so eruncommon.ResolveOpen succeeds; the
// exact port only needs to be deterministic, not any particular value.
func jobsTestStore(t *testing.T) stubUIStore {
	t.Helper()
	return stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "ux"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/ux": {
				Name:              "ux",
				LocalRepoPath:     t.TempDir(),
				KubernetesContext: "orbstack",
			},
		},
	}
}

// TestLoadEnvironmentJobsReadsThroughAReachablePodEdge is the regression test
// for the desktop reading only its own host's job store: a remote-agent
// environment's jobs run in its pod, so when the pod is reachable the job the
// pod itself reports must be what the Jobs tab sees, not an empty local
// directory that has nothing to do with that pod. Before the fix this failed
// because LoadEnvironmentJobs never consulted the MCP edge at all -- it always
// read the (here, empty) local store and returned no jobs.
func TestLoadEnvironmentJobsReadsThroughAReachablePodEdge(t *testing.T) {
	isolateJobsCache(t)
	var gotEndpoint string
	app := NewApp(erunUIDeps{
		store: jobsTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return true
		},
		loadEnvironmentJobs: func(_ context.Context, endpoint, _ string) ([]eruncommon.EnvironmentJob, error) {
			gotEndpoint = endpoint
			return []eruncommon.EnvironmentJob{
				{ID: "finish-1350", Name: "Finish #1350 desktop jobs surface", State: eruncommon.EnvironmentJobStateRunning},
			}, nil
		},
	})

	jobs, err := app.LoadEnvironmentJobs("erun", "ux")
	if err != nil {
		t.Fatalf("LoadEnvironmentJobs failed: %v", err)
	}
	if len(jobs) != 1 || jobs[0].ID != "finish-1350" {
		t.Fatalf("expected the pod's own job to be reported, got %+v", jobs)
	}
	if gotEndpoint == "" {
		t.Fatalf("expected the pod-backed reader to be used")
	}
}

// TestLoadEnvironmentJobsReportsStaleForwardInsteadOfEmpty is the sibling
// regression: a stale port-forward (something holds the port, the edge never
// answers) means a pod may well be running jobs that cannot currently be
// asked about. Silently falling back to the (here, empty) local store would
// render the same "No jobs yet" the operator cannot distinguish from a
// genuinely idle environment, so this must surface as an unreachable error
// instead.
func TestLoadEnvironmentJobsReportsStaleForwardInsteadOfEmpty(t *testing.T) {
	isolateJobsCache(t)
	app := NewApp(erunUIDeps{
		store: jobsTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		canConnectLocalPort: func(int) bool {
			return true
		},
	})

	jobs, err := app.LoadEnvironmentJobs("erun", "ux")
	if err == nil {
		t.Fatalf("expected an unreachable error for a stale forward, got jobs=%+v", jobs)
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if !strings.Contains(err.Error(), "ERUN_MCP_UNREACHABLE_STALE: ") {
		t.Fatalf("expected the stale-forward reachability marker, got %v", err)
	}
}

// TestLoadEnvironmentJobsFallsBackToLocalWhenNotOpen preserves the case the
// fix must not break: an environment nobody has opened has no pod running, so
// the local store -- where the desktop's own Investigate job for that
// environment is recorded, regardless of the environment's type -- is the
// real (not a consolation) answer.
func TestLoadEnvironmentJobsFallsBackToLocalWhenNotOpen(t *testing.T) {
	isolateJobsCache(t)
	if _, err := eruncommon.AttachEnvironmentJob(eruncommon.Context{}, eruncommon.AttachEnvironmentJobParams{
		Tenant:      "erun",
		Environment: "ux",
		Name:        "Investigate ux",
		PID:         os.Getpid(),
	}); err != nil {
		t.Fatalf("attach investigate job: %v", err)
	}

	app := NewApp(erunUIDeps{
		store: jobsTestStore(t),
		canReachMCPEndpoint: func(int) bool {
			return false
		},
		canConnectLocalPort: func(int) bool {
			return false
		},
	})

	jobs, err := app.LoadEnvironmentJobs("erun", "ux")
	if err != nil {
		t.Fatalf("LoadEnvironmentJobs failed: %v", err)
	}
	if len(jobs) != 1 || !strings.Contains(jobs[0].Name, "Investigate") {
		t.Fatalf("expected the host-recorded investigate job to survive the fallback, got %+v", jobs)
	}
}

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
		!strings.Contains(err.Error(), "tenant not set") {
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
