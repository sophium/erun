package main

import (
	"context"
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
// repo or gh.
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
