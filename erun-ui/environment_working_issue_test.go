package main

import (
	"context"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
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

// workingIssueApp builds an App whose store resolves one env and whose
// working-issue command runner is stubbed, so the resolver runs without a real
// repo or gh. The pod-path deps are pinned hermetic (issue #492): a remote
// env resolved through this helper must never probe the developer machine's
// real local ports — ResolveOpen allocates a port range even when none is
// persisted, and a live desktop/headless erun can be listening there, turning
// the default reachability probe + MCP call into real network traffic. Tests
// that exercise the pod-backed path inject their own deps via
// remoteWorkingIssueApp.
func workingIssueApp(t *testing.T, env eruncommon.EnvConfig, run workingIssueCommandRunner) *App {
	t.Helper()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"acme": {Name: "acme", ProjectRoot: "/tmp/acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"acme/dev": env,
		},
	}
	app := NewApp(erunUIDeps{
		store:                  store,
		findProjectRoot:        func() (string, string, error) { return "acme", "/tmp/acme", nil },
		runWorkingIssueCommand: run,
		canConnectLocalPort:    func(int) bool { return false },
		loadPodBranch: func(context.Context, string) (string, error) {
			t.Fatal("loadPodBranch must not run through the legacy working-issue helper")
			return "", nil
		},
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

func TestEnvironmentWorkingIssueResolvesBranchAndTitle(t *testing.T) {
	var calls [][]string
	run := func(_ context.Context, dir, name string, args ...string) (string, error) {
		calls = append(calls, append([]string{name}, args...))
		switch {
		case name == "git":
			return "feature/437-sidebar-env-hover", nil
		case name == "gh":
			return "Sidebar environment hover", nil
		}
		return "", nil
	}
	app := workingIssueApp(t, eruncommon.EnvConfig{
		Name:              "dev",
		Type:              eruncommon.EnvironmentTypeLocalAgent,
		KubernetesContext: "ctx",
		LocalRepoPath:     t.TempDir(),
	}, run)

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if !got.Available {
		t.Fatalf("expected Available for a local-agent env, got %+v", got)
	}
	if got.Branch != "feature/437-sidebar-env-hover" || got.IssueNumber != 437 || got.IssueTitle != "Sidebar environment hover" {
		t.Fatalf("unexpected working issue: %+v", got)
	}
	if len(calls) != 2 {
		t.Fatalf("expected git + gh (2 calls), got %d: %v", len(calls), calls)
	}

	// Second call within the TTL is served from cache (no extra git/gh).
	if _, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"}); err != nil {
		t.Fatalf("second EnvironmentWorkingIssue: %v", err)
	}
	if len(calls) != 2 {
		t.Errorf("expected cache hit on second call (still 2 calls), got %d: %v", len(calls), calls)
	}
}

func TestEnvironmentWorkingIssueUnavailableForRemoteWorktree(t *testing.T) {
	run := func(context.Context, string, string, ...string) (string, error) {
		t.Fatalf("runner must not be invoked for a remote-worktree env")
		return "", nil
	}
	app := workingIssueApp(t, eruncommon.EnvConfig{
		Name:              "dev",
		Type:              eruncommon.EnvironmentTypeRemoteAgent,
		KubernetesContext: "ctx",
	}, run)

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
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

// remoteWorkingIssueApp builds an App for the pod-backed resolution paths
// (issue #462): a remote-agent env with a port range, an injectable
// reachability answer, and an injectable in-pod branch loader.
func remoteWorkingIssueApp(t *testing.T, reachable bool, loadPodBranch func(context.Context, string) (string, error), run workingIssueCommandRunner) *App {
	t.Helper()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"acme": {Name: "acme", ProjectRoot: "/tmp/acme", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"acme/dev": {
				Name:                "dev",
				Type:                eruncommon.EnvironmentTypeRemoteAgent,
				KubernetesContext:   "ctx",
				LocalPortRangeStart: 17500,
			},
		},
	}
	app := NewApp(erunUIDeps{
		store:                  store,
		findProjectRoot:        func() (string, string, error) { return "acme", "/tmp/acme", nil },
		canConnectLocalPort:    func(int) bool { return reachable },
		loadPodBranch:          loadPodBranch,
		runWorkingIssueCommand: run,
	})
	t.Cleanup(func() { app.shutdown(context.Background()) })
	return app
}

func TestEnvironmentWorkingIssueRemoteNotReachable(t *testing.T) {
	app := remoteWorkingIssueApp(t, false,
		func(context.Context, string) (string, error) {
			t.Fatal("loadPodBranch must not run while the env is not reachable")
			return "", nil
		},
		func(context.Context, string, string, ...string) (string, error) {
			t.Fatal("runner must not run while the env is not reachable")
			return "", nil
		},
	)

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
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
	app := remoteWorkingIssueApp(t, true,
		func(context.Context, string) (string, error) {
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

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
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
	app := remoteWorkingIssueApp(t, true,
		func(context.Context, string) (string, error) {
			loads++
			return "", context.DeadlineExceeded
		},
		func(context.Context, string, string, ...string) (string, error) {
			t.Fatal("runner must not run when the pod branch failed to load")
			return "", nil
		},
	)

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if got.Available || !strings.Contains(got.Reason, "not reachable right now") {
		t.Fatalf("expected the not-reachable reason, got %+v", got)
	}
	// Unavailable answers must not be cached: the next hover after the env
	// recovers has to re-resolve instead of replaying the failure for the
	// TTL.
	if _, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"}); err != nil {
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
	app := workingIssueApp(t, eruncommon.EnvConfig{
		Name:              "dev",
		Type:              eruncommon.EnvironmentTypeLocalAgent,
		KubernetesContext: "ctx",
		LocalRepoPath:     t.TempDir(),
	}, run)

	got, err := app.EnvironmentWorkingIssue(uiSelection{Tenant: "acme", Environment: "dev"})
	if err != nil {
		t.Fatalf("EnvironmentWorkingIssue: %v", err)
	}
	if !got.Available || got.Branch != "main" || got.IssueNumber != 0 || got.IssueTitle != "" {
		t.Fatalf("expected available branch=main with no issue, got %+v", got)
	}
}
