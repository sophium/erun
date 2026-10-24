package main

import (
	"context"
	"os"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// Host, local-agent, and remote-agent envs are eligible without a role.
func TestOrchestratableEnvCoversHost(t *testing.T) {
	cases := []struct {
		envType configpkg.EnvironmentType
		want    bool
	}{
		{configpkg.EnvironmentTypeLocalAgent, true},
		{configpkg.EnvironmentTypeRemoteAgent, true},
		{configpkg.EnvironmentTypeHost, true},
		{"", false},
	}
	for _, tc := range cases {
		env := configpkg.EnvConfig{Type: tc.envType}
		if got := orchestratableEnv(env, ""); got != tc.want {
			t.Errorf("orchestratableEnv(type=%q, role=undeclared) = %v, want %v", tc.envType, got, tc.want)
		}
		if got := orchestratableEnv(env, configpkg.OrchestratorEnvRoleCode); got != tc.want {
			t.Errorf("orchestratableEnv(type=%q, role=code) = %v, want %v", tc.envType, got, tc.want)
		}
	}
}

func TestOrchestratableEnvGatesRuntimeOnTheRuntimeRole(t *testing.T) {
	env := configpkg.EnvConfig{Type: configpkg.EnvironmentTypeRuntime}
	cases := []struct {
		role configpkg.OrchestratorEnvRole
		want bool
	}{
		{configpkg.OrchestratorEnvRoleRuntime, true},
		{configpkg.OrchestratorEnvRoleCode, false},
		{configpkg.OrchestratorEnvRoleBuild, false},
		{"", false},
	}
	for _, tc := range cases {
		if got := orchestratableEnv(env, tc.role); got != tc.want {
			t.Errorf("orchestratableEnv(runtime, role=%q) = %v, want %v", tc.role, got, tc.want)
		}
	}
}

func TestOrchestratorReviewDirectoryReviewsAHostEnvInPlace(t *testing.T) {
	env := configpkg.EnvConfig{Type: configpkg.EnvironmentTypeHost, LocalRepoPath: "/home/erun/work"}
	dir, mirrored := orchestratorReviewDirectory("frs", env)
	if dir != "/home/erun/work" || mirrored {
		t.Fatalf("orchestratorReviewDirectory(host) = (%q, %v), want (%q, false)", dir, mirrored, "/home/erun/work")
	}
}

// A runtime link must not fall through to the remote-agent mirror default.
func TestOrchestratorReviewDirectoryHasNoneForARuntimeEnv(t *testing.T) {
	env := configpkg.EnvConfig{Type: configpkg.EnvironmentTypeRuntime}
	dir, mirrored := orchestratorReviewDirectory("frs", env)
	if dir != "" || mirrored {
		t.Fatalf("orchestratorReviewDirectory(runtime) = (%q, %v), want (\"\", false)", dir, mirrored)
	}
}

// listOrchestratorTestCandidates returns dev (remote-agent), laptop (local-agent), runtime.
func listOrchestratorTestCandidates(t *testing.T) (dev, laptop, rt orchestratorEnvCandidate, laptopRepo string) {
	t.Helper()
	app, repo := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	candidates, err := app.ListOrchestratorEnvCandidates()
	if err != nil {
		t.Fatalf("ListOrchestratorEnvCandidates failed: %v", err)
	}
	if len(candidates) != 3 {
		t.Fatalf("expected both agent envs plus the runtime env, got %+v", candidates)
	}
	dev, laptop, rt = candidates[0], candidates[1], candidates[2]
	if dev.Environment != "dev" || laptop.Environment != "laptop" || rt.Environment != "runtime" {
		t.Fatalf("expected dev, laptop, runtime in that order, got %+v", candidates)
	}
	return dev, laptop, rt, repo
}

func TestListOrchestratorEnvCandidatesReviewsARemoteAgentEnvInAMirror(t *testing.T) {
	dev, _, _, _ := listOrchestratorTestCandidates(t)
	if !dev.Eligible || !dev.Mirrored {
		t.Fatalf("expected the remote-agent env to be eligible and reviewed in a mirror, got %+v", dev)
	}
	if !strings.HasSuffix(dev.DefaultDirectory, "orchestrators"+string(os.PathSeparator)+"frs-dev") {
		t.Fatalf("unexpected mirror directory: %q", dev.DefaultDirectory)
	}
}

func TestListOrchestratorEnvCandidatesReviewsALocalAgentEnvInPlace(t *testing.T) {
	_, laptop, _, laptopRepo := listOrchestratorTestCandidates(t)
	if !laptop.Eligible || laptop.Mirrored {
		t.Fatalf("expected the local-agent env to be eligible and reviewed in place, got %+v", laptop)
	}
	if laptop.DefaultDirectory != laptopRepo {
		t.Fatalf("local-agent review directory = %q, want the env worktree %q", laptop.DefaultDirectory, laptopRepo)
	}
}

func TestListOrchestratorEnvCandidatesOffersARuntimeEnvForTheRuntimeRole(t *testing.T) {
	_, _, rt, _ := listOrchestratorTestCandidates(t)
	if !rt.Eligible {
		t.Fatalf("expected the runtime env to stay eligible, got %+v", rt)
	}
	if rt.DefaultDirectory != "" || rt.Mirrored {
		t.Fatalf("expected the runtime env to carry no review directory, got %+v", rt)
	}
	if rt.RequiredRole != configpkg.OrchestratorEnvRoleRuntime {
		t.Fatalf("expected the runtime env to require the runtime role, got %+v", rt)
	}
	if rt.IneligibleReason != "" {
		t.Fatalf("expected the eligible runtime env to carry no reason, got %+v", rt)
	}
}

func TestOrchestratorIneligibilityReasonExplainsEachRejectedType(t *testing.T) {
	cases := []struct {
		envType    configpkg.EnvironmentType
		role       configpkg.OrchestratorEnvRole
		wantEmpty  bool
		wantSubstr string
	}{
		{configpkg.EnvironmentTypeLocalAgent, "", true, ""},
		{configpkg.EnvironmentTypeRemoteAgent, "", true, ""},
		{configpkg.EnvironmentTypeHost, "", true, ""},
		{configpkg.EnvironmentTypeRuntime, configpkg.OrchestratorEnvRoleRuntime, true, ""},
		{configpkg.EnvironmentTypeRuntime, configpkg.OrchestratorEnvRoleCode, false, "no worktree"},
		{configpkg.EnvironmentTypeRuntime, "", false, "runtime role"},
		{"", "", false, "type"},
	}
	for _, tc := range cases {
		got := orchestratorIneligibilityReason(configpkg.EnvConfig{Type: tc.envType}, tc.role)
		if tc.wantEmpty && got != "" {
			t.Errorf("orchestratorIneligibilityReason(type=%q, role=%q) = %q, want empty", tc.envType, tc.role, got)
		}
		if !tc.wantEmpty && !strings.Contains(got, tc.wantSubstr) {
			t.Errorf("orchestratorIneligibilityReason(type=%q, role=%q) = %q, want substring %q", tc.envType, tc.role, got, tc.wantSubstr)
		}
	}
}
