package main

import (
	"context"
	"os"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// The three TestListOrchestratorEnvCandidates* tests below lock the
// capability: an orchestrator can link either agent type, and each candidate
// carries where that env is reviewed — a mirror the operator may place
// anywhere, or the local-agent worktree already on this machine. A runtime
// env is still listed, disabled, with a reason rather than dropped without
// trace.
// TestOrchestratableEnvCoversHost locks in that a host env links like
// local-agent and remote-agent: a host env's worktree is already the operator's
// own checkout, so the orchestrator works it in place, the same as a
// local-agent worktree, never a synced mirror (which only makes sense for a pod
// whose worktree lives somewhere else). None of these three types requires a
// particular role, so all three are eligible undeclared; a host env is still
// refused the runtime role when one is chosen, but that is the shared gate's
// question, not eligibility's. A runtime env is the odd one out and is covered
// by its own test below (TestOrchestratableEnvGatesRuntimeOnTheRuntimeRole)
// since its answer depends on the role, not just the type.
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

// TestOrchestratableEnvGatesRuntimeOnTheRuntimeRole locks the fix: a runtime
// env, which has no worktree to review and no in-pod agent to delegate to,
// may only be linked with the runtime role. code, build, and undeclared are
// all still refused — the same refusal the gate previously applied
// unconditionally, now narrowed to every role except runtime rather than
// dropped.
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

// TestOrchestratorReviewDirectoryReviewsAHostEnvInPlace mirrors the
// local-agent case: a host env's review directory is exactly its local repo
// path, with mirrored=false — there is no pod to sync from.
func TestOrchestratorReviewDirectoryReviewsAHostEnvInPlace(t *testing.T) {
	env := configpkg.EnvConfig{Type: configpkg.EnvironmentTypeHost, LocalRepoPath: "/home/erun/work"}
	dir, mirrored := orchestratorReviewDirectory("frs", env)
	if dir != "/home/erun/work" || mirrored {
		t.Fatalf("orchestratorReviewDirectory(host) = (%q, %v), want (%q, false)", dir, mirrored, "/home/erun/work")
	}
}

// TestOrchestratorReviewDirectoryHasNoneForARuntimeEnv locks scope item 3: a
// runtime-role link resolves no review directory, asserted on the resolved
// value itself rather than the absence of an error — it must not fall
// through to the mirror default meant for a remote-agent env that does have
// a pod to sync a mirror from.
func TestOrchestratorReviewDirectoryHasNoneForARuntimeEnv(t *testing.T) {
	env := configpkg.EnvConfig{Type: configpkg.EnvironmentTypeRuntime}
	dir, mirrored := orchestratorReviewDirectory("frs", env)
	if dir != "" || mirrored {
		t.Fatalf("orchestratorReviewDirectory(runtime) = (%q, %v), want (\"\", false)", dir, mirrored)
	}
}

// listOrchestratorTestCandidates is the shared fixture read for the
// candidate-shape assertions below: dev (remote-agent), laptop (local-agent),
// runtime (eligible only for the runtime role), in that sorted order.
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

// TestListOrchestratorEnvCandidatesOffersARuntimeEnvForTheRuntimeRole locks
// scope item 5: a runtime env is no longer greyed out in the picker. It stays
// eligible (the dialog offers the role picker for it, not the ineligibility
// notice), carries no review directory or mirror flag, and names the one
// role — runtime — the operator must pick for it to actually link.
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

// TestOrchestratorIneligibilityReasonExplainsEachRejectedType locks the
// operator-facing copy: an eligible env/role pair carries no reason, a
// runtime env refused a non-runtime role names the concrete gap (no
// worktree, no in-pod agent) and points at the role that does work, and an
// unresolved type still gets an actionable — if generic — explanation rather
// than "".
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
