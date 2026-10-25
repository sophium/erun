package runtimeview

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestParseIssueNumberFromBranch(t *testing.T) {
	cases := map[string]int{
		"feature/437-sidebar-env-hover": 437,
		"bug/12-fix-thing":              12,
		"feature/5-x":                   5,
		"main":                          0,
		"develop":                       0,
		"feature/no-number":             0,
		"hotfix/9-x":                    0, // only feature/ and bug/ per the convention
		"":                              0,
		"HEAD":                          0,
	}
	for branch, want := range cases {
		if got := parseIssueNumberFromBranch(branch); got != want {
			t.Errorf("parseIssueNumberFromBranch(%q) = %d, want %d", branch, got, want)
		}
	}
}

// workingIssueService pins the local port unreachable so tests never probe real ports.
func workingIssueService(t *testing.T, env config.EnvConfig, run CommandRunner) *Service {
	t.Helper()
	store := fakeStore{
		tenants: map[string]model.TenantConfig{
			"acme": {Name: "acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"acme/dev": env,
		},
	}
	return newTestService(Deps{
		Store:               store,
		RunCommand:          run,
		CanConnectLocalPort: func(int) bool { return false },
		CanReachMCPEndpoint: func(int) bool { return false },
		LoadPodBranch: func(context.Context, string, string) (string, error) {
			t.Fatal("loadPodBranch must not run through the legacy working-issue helper")
			return "", nil
		},
	})
}

func TestEnvironmentWorkingIssueResolvesBranchAndTitle(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, dir, name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		switch name {
		case "git":
			return "feature/437-sidebar-env-hover", nil
		case "gh":
			return "Sidebar environment hover", nil
		}
		return "", nil
	}
	service := workingIssueService(t, config.EnvConfig{
		Name:              "dev",
		Type:              config.EnvironmentTypeLocalAgent,
		KubernetesContext: "ctx",
		LocalRepoPath:     t.TempDir(),
	}, run)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	assertResolvedWorkingIssue(t, got)
	if len(calls) != 2 {
		t.Fatalf("expected git + gh (2 calls), got %d: %v", len(calls), calls)
	}

	if _, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"}); err != nil {
		t.Fatalf("second EnvironmentWorkingIssue: %v", err)
	}
	if len(calls) != 2 {
		t.Errorf("expected cache hit on second call (still 2 calls), got %d: %v", len(calls), calls)
	}
}

func assertResolvedWorkingIssue(t *testing.T, got WorkingIssue) {
	t.Helper()

	if !got.Available {
		t.Fatalf("expected Available for a local-agent env, got %+v", got)
	}
	if got.Branch != "feature/437-sidebar-env-hover" || got.IssueNumber != 437 || got.IssueTitle != "Sidebar environment hover" {
		t.Fatalf("unexpected working issue: %+v", got)
	}
}

func TestEnvironmentWorkingIssueUnavailableForRemoteWorktree(t *testing.T) {
	run := func(context.Context, string, string, ...string) (string, error) {
		t.Fatalf("runner must not be invoked for a remote-worktree env")
		return "", nil
	}
	service := workingIssueService(t, config.EnvConfig{
		Name:              "dev",
		LocalRepoPath:     "/home/erun/git/acme",
		Type:              config.EnvironmentTypeRemoteAgent,
		KubernetesContext: "ctx",
	}, run)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if got.Available {
		t.Fatalf("expected unavailable for remote-worktree env, got %+v", got)
	}
	if got.Reason == "" {
		t.Errorf("expected a reason explaining why it's unavailable")
	}
}

func remoteWorkingIssueService(t *testing.T, reachable bool, loadPodBranch func(context.Context, string, string) (string, error), run CommandRunner) *Service {
	t.Helper()
	store := fakeStore{
		tenants: map[string]model.TenantConfig{
			"acme": {Name: "acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"acme/dev": {
				Name:                "dev",
				LocalRepoPath:       "/home/erun/git/acme",
				Type:                config.EnvironmentTypeRemoteAgent,
				KubernetesContext:   "ctx",
				LocalPortRangeStart: 17500,
			},
		},
	}
	return newTestService(Deps{
		Store:               store,
		CanConnectLocalPort: func(int) bool { return reachable },
		CanReachMCPEndpoint: func(int) bool { return reachable },
		LoadPodBranch:       loadPodBranch,
		RunCommand:          run,
	})
}

func TestEnvironmentWorkingIssueRemoteNotReachable(t *testing.T) {
	service := remoteWorkingIssueService(t, false,
		func(context.Context, string, string) (string, error) {
			t.Fatal("loadPodBranch must not run while the env is not reachable")
			return "", nil
		},
		func(context.Context, string, string, ...string) (string, error) {
			t.Fatal("runner must not run while the env is not reachable")
			return "", nil
		},
	)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if got.Available {
		t.Fatalf("expected unavailable while unreachable, got %+v", got)
	}
	if !strings.Contains(got.Reason, "open this environment") {
		t.Fatalf("expected the open-to-view reason, got %q", got.Reason)
	}
}

func TestEnvironmentWorkingIssueRemoteReadsPodBranch(t *testing.T) {
	var ghDirs []string
	service := remoteWorkingIssueService(t, true,
		func(context.Context, string, string) (string, error) {
			return "bug/470-sidebar-status", nil
		},
		func(_ context.Context, dir, name string, _ ...string) (string, error) {
			if name != "gh" {
				t.Fatalf("only the gh title lookup may run host-side for a remote env, got %q", name)
			}
			ghDirs = append(ghDirs, dir)
			return "Sidebar status", nil
		},
	)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if !got.Available || got.Branch != "bug/470-sidebar-status" || got.IssueNumber != 470 || got.IssueTitle != "Sidebar status" {
		t.Fatalf("unexpected pod-backed working issue: %+v", got)
	}
	if len(ghDirs) != 1 || ghDirs[0] == "" {
		t.Fatalf("expected one gh lookup rooted in the host project worktree, got %v", ghDirs)
	}
}

func TestEnvironmentWorkingIssueRemotePodErrorNotCached(t *testing.T) {
	var loads int
	service := remoteWorkingIssueService(t, true,
		func(context.Context, string, string) (string, error) {
			loads++
			return "", context.DeadlineExceeded
		},
		func(context.Context, string, string, ...string) (string, error) {
			t.Fatal("runner must not run when the pod branch failed to load")
			return "", nil
		},
	)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if got.Available || !strings.Contains(got.Reason, "not reachable right now") {
		t.Fatalf("expected the not-reachable reason, got %+v", got)
	}
	if _, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"}); err != nil {
		t.Fatalf("second EnvironmentWorkingIssue: %v", err)
	}
	if loads != 2 {
		t.Fatalf("expected the failed resolution to re-run (2 loads), got %d", loads)
	}
}

func TestEnvironmentWorkingIssueBranchWithoutIssue(t *testing.T) {
	run := func(_ context.Context, _ string, name string, _ ...string) (string, error) {
		if name == "gh" {
			t.Fatalf("gh must not run when the branch names no issue")
		}
		return "main", nil
	}
	service := workingIssueService(t, config.EnvConfig{
		Name:              "dev",
		Type:              config.EnvironmentTypeLocalAgent,
		KubernetesContext: "ctx",
		LocalRepoPath:     t.TempDir(),
	}, run)

	got, err := service.EnvironmentWorkingIssue(uimodel.Selection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if !got.Available || got.Branch != "main" || got.IssueNumber != 0 || got.IssueTitle != "" {
		t.Fatalf("expected available branch=main with no issue, got %+v", got)
	}
}
