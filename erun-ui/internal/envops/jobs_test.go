package envops

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/adrg/xdg"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

// isolateJobsCache needs xdg.Reload because xdg resolves XDG_CACHE_HOME at init.
func isolateJobsCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func jobsTestStore(t *testing.T) fakeStore {
	t.Helper()
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "ux"},
		},
		envs: map[string]config.EnvConfig{
			"erun/ux": {
				Name:              "ux",
				LocalRepoPath:     t.TempDir(),
				KubernetesContext: "orbstack",
			},
		},
	}
}

func TestLoadEnvironmentJobsReadsThroughAReachablePodEdge(t *testing.T) {
	isolateJobsCache(t)
	var gotEndpoint string
	service := newTestService(Deps{
		Store:               jobsTestStore(t),
		CanReachMCPEndpoint: func(int) bool { return true },
		LoadJobs: func(_ context.Context, endpoint, _ string) ([]jobstate.EnvironmentJob, error) {
			gotEndpoint = endpoint
			return []jobstate.EnvironmentJob{
				{ID: "finish-1350", Name: "Finish #1350 desktop jobs surface", State: jobspkg.EnvironmentJobStateRunning},
			}, nil
		},
	})

	jobs, err := service.LoadEnvironmentJobs("erun", "ux")
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

func TestLoadEnvironmentJobsReportsStaleForwardInsteadOfEmpty(t *testing.T) {
	isolateJobsCache(t)
	service := newTestService(Deps{
		Store:               jobsTestStore(t),
		CanReachMCPEndpoint: func(int) bool { return false },
		CanConnectLocalPort: func(int) bool { return true },
	})

	jobs, err := service.LoadEnvironmentJobs("erun", "ux")
	if err == nil {
		t.Fatalf("expected an unreachable error for a stale forward, got jobs=%+v", jobs)
	}
	var unreachable unreachableError
	if !errors.As(err, &unreachable) || !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected an unreachable error, got %v", err)
	}
	if unreachable.kind != mcp.LocalMCPStaleForward {
		t.Fatalf("expected the stale-forward kind, got %v", unreachable.kind)
	}
}

func TestLoadEnvironmentJobsFallsBackToLocalWhenNotOpen(t *testing.T) {
	isolateJobsCache(t)
	if _, err := jobspkg.AttachEnvironmentJob(core.Context{}, model.AttachEnvironmentJobParams{
		Tenant:      "erun",
		Environment: "ux",
		Name:        "Investigate ux",
		PID:         os.Getpid(),
	}); err != nil {
		t.Fatalf("attach investigate job: %v", err)
	}

	service := newTestService(Deps{
		Store:               jobsTestStore(t),
		CanReachMCPEndpoint: func(int) bool { return false },
		CanConnectLocalPort: func(int) bool { return false },
	})

	jobs, err := service.LoadEnvironmentJobs("erun", "ux")
	if err != nil {
		t.Fatalf("LoadEnvironmentJobs failed: %v", err)
	}
	if len(jobs) != 1 || !strings.Contains(jobs[0].Name, "Investigate") {
		t.Fatalf("expected the host-recorded investigate job to survive the fallback, got %+v", jobs)
	}
}

func TestJobToUIKeepsAbsentOutcomesAbsent(t *testing.T) {
	running := environmentJobToUI(jobstate.EnvironmentJob{
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
	exited := environmentJobToUI(jobstate.EnvironmentJob{
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
	service := newTestService(Deps{})
	if _, err := service.LoadEnvironmentJobs(" ", "build"); err == nil ||
		!strings.Contains(err.Error(), "tenant not set") {
		t.Fatalf("blank tenant: %v", err)
	}
	if _, err := service.ReadEnvironmentJobOutput(JobOutputInput{Tenant: "t", Environment: "e"}); err == nil ||
		!strings.Contains(err.Error(), "job id are required") {
		t.Fatalf("blank job id: %v", err)
	}
	if _, err := service.CancelEnvironmentJob(CancelJobInput{Tenant: "t", Environment: "e"}); err == nil {
		t.Fatal("cancel without a job id must be refused")
	}
}
