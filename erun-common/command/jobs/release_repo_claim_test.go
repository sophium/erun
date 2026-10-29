package jobs

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseclaim"
	"github.com/sophium/erun/erun-common/primitive/shared/releaseflow"
)

// The repository-global release claim, exercised from two checkouts of a real
// remote so a scope mistake cannot pass by construction.

func newTestClaimContext() core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(0, io.Discard, io.Discard)}
}

// cloneRepoForTest stands in for a second environment's worktree of the same repository.
func cloneRepoForTest(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	runGitForTest(t, filepath.Dir(dir), "clone", "-q", "--branch", "main", remote, dir)
	runGitForTest(t, dir, "config", "user.email", "test@example.com")
	runGitForTest(t, dir, "config", "user.name", "Test")
	return dir
}

func TestReleaseRepoClaimRefusesASecondReleaseFromADifferentCheckoutAndNamesTheHolder(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	sha, err := releaseclaim.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, now, nil)
	if err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	holderB := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err = releaseclaim.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, now.Add(time.Second), nil)
	if err == nil {
		t.Fatal("expected a second release of the same version from a different checkout to be refused")
	}
	if !strings.Contains(err.Error(), "1.0.213") {
		t.Errorf("refusal must name the version, got: %v", err)
	}
	if !strings.Contains(err.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the holder so the second environment knows who to wait on, got: %v", err)
	}
	if !strings.Contains(err.Error(), "build-a") {
		t.Errorf("refusal must name the losing caller's counterpart environment, got: %v", err)
	}

	// The claim is scoped to the version's ref, not the whole repository.
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.214", holderB, now.Add(time.Second), nil); err != nil {
		t.Fatalf("a release of a different version must not collide with an unrelated one in flight: %v", err)
	}
}

// Without ERUN_ORCHESTRATOR_ID the refusal must still name the environment.
func TestReleaseRepoClaimRefusalNamesTheEnvironmentWhenTheOrchestratorIDIsUnset(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	now := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holder := model.EnvironmentActivityLeaseHolder{Tenant: "erun"}
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, envA, "build", "1.0.215", holder, now, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	_, err := releaseclaim.TakeReleaseRepoClaim(ctx, envB, "release", "1.0.215", holder, now.Add(time.Second), nil)
	if err == nil {
		t.Fatal("expected a second release of the same version from a different environment to be refused")
	}
	if !strings.Contains(err.Error(), "environment build") {
		t.Errorf("refusal must name the losing caller's counterpart environment, not just its tenant, got: %v", err)
	}
}

func TestReleaseRepoClaimReclaimsAnAbandonedReleaseFromADifferentCheckout(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.213", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	// envA's claim was never released; only lapsing can free it.
	afterLapse := start.Add(releaseclaim.ReleaseVersionClaimTTL + time.Minute)
	holderB := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := releaseclaim.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.213", holderB, afterLapse, nil)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}
	if sha == "" {
		t.Fatalf("expected a real repository claim, got an inconclusive no-op")
	}

	remoteSHA, exists, err := releaseclaim.GitLsRemoteRef(ctx, envB, releaseclaim.ReleaseRepoClaimRemote, releaseclaim.ReleaseRepoClaimRef("1.0.213"))
	if err != nil || !exists {
		t.Fatalf("expected the reclaimed ref to be published, exists=%v err=%v", exists, err)
	}
	if remoteSHA != sha {
		t.Fatalf("remote claim ref = %s, want the reclaiming holder's sha %s", remoteSHA, sha)
	}
}

// exitedProcessPIDForTest returns a real pid whose process has exited.
func exitedProcessPIDForTest(t *testing.T) int {
	t.Helper()
	cmd := core.Command("git", "--version")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start a stand-in process: %v", err)
	}
	pid := cmd.Process.Pid
	if err := cmd.Wait(); err != nil {
		t.Fatalf("wait for the stand-in process: %v", err)
	}
	return pid
}

// A live local holder must keep refusing a second release here.
func TestReleaseRepoClaimStillRefusesWhileThisEnvironmentsHolderIsAlive(t *testing.T) {
	isolateActivityCache(t)
	repo := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	const tenant, environment, version = "erun", "build", "1.0.289"
	start := time.Date(2026, 9, 21, 9, 0, 0, 0, time.UTC)
	ctx := newTestClaimContext()

	running := model.EnvironmentActivityLeaseHolder{Tenant: tenant, Orchestrator: "orchestrator-a"}
	if _, err := releaseclaim.TakeReleaseVersionClaim(tenant, environment, version, running, os.Getpid(), start); err != nil {
		t.Fatalf("running release's local claim: %v", err)
	}
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, repo, environment, version, running, start, nil); err != nil {
		t.Fatalf("running release's repository claim: %v", err)
	}

	retry := start.Add(time.Minute)
	localHolderAlive := func() bool {
		held, err := releaseclaim.EnvironmentHasLiveReleaseHolder(tenant, environment, version, retry)
		if err != nil {
			t.Fatalf("reading this environment's release claims: %v", err)
		}
		return held
	}
	if !localHolderAlive() {
		t.Fatal("a live local holder must read as present")
	}

	second := model.EnvironmentActivityLeaseHolder{Tenant: tenant, Orchestrator: "orchestrator-b"}
	_, err := releaseclaim.TakeReleaseRepoClaim(ctx, repo, environment, version, second, retry, localHolderAlive)
	if err == nil {
		t.Fatal("expected a second release in the same environment to be refused while the first is running")
	}
	if !strings.Contains(err.Error(), "orchestrator-a") {
		t.Errorf("refusal must name the running holder, got: %v", err)
	}
}

func TestReleaseRepoClaimFreshClaimSetsStartedAtToNow(t *testing.T) {
	env := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, env)
	runGitForTest(t, env, "push", "-q", "-u", "origin", "main")

	now := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()
	holder := model.EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := releaseclaim.TakeReleaseRepoClaim(ctx, env, "build", "1.0.220", holder, now, nil)
	if err != nil {
		t.Fatalf("fresh claim: %v", err)
	}
	record, err := releaseclaim.ReadReleaseRepoClaimBlob(ctx, env, sha)
	if err != nil {
		t.Fatalf("reading back the claim blob: %v", err)
	}
	if !record.StartedAt.Equal(now) {
		t.Errorf("fresh claim StartedAt = %v, want %v", record.StartedAt, now)
	}
}

// Renewal must not rewrite StartedAt.
func TestReleaseRepoClaimRenewalPreservesStartedAtAndAdvancesOnlyExpiresAt(t *testing.T) {
	env := newAgentJobTestRepo(t)
	newBareRemoteForTest(t, env)
	runGitForTest(t, env, "push", "-q", "-u", "origin", "main")

	started := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()
	holder := model.EnvironmentActivityLeaseHolder{Tenant: "erun"}

	sha, err := releaseclaim.TakeReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, started, nil)
	if err != nil {
		t.Fatalf("initial claim: %v", err)
	}

	renewedAt := started.Add(13 * time.Minute)
	newSHA, err := releaseclaim.RenewReleaseRepoClaim(ctx, env, "ux", "1.0.220", holder, renewedAt, sha)
	if err != nil {
		t.Fatalf("renewal: %v", err)
	}

	record, err := releaseclaim.ReadReleaseRepoClaimBlob(ctx, env, newSHA)
	if err != nil {
		t.Fatalf("reading back the renewed claim blob: %v", err)
	}
	if !record.StartedAt.Equal(started) {
		t.Errorf("renewal StartedAt = %v, want the original %v preserved", record.StartedAt, started)
	}
	wantExpiresAt := renewedAt.Add(releaseclaim.ReleaseVersionClaimTTL)
	if !record.ExpiresAt.Equal(wantExpiresAt) {
		t.Errorf("renewal ExpiresAt = %v, want %v", record.ExpiresAt, wantExpiresAt)
	}
}

// A reclaim is a new holder and must not inherit StartedAt.
func TestReleaseRepoClaimReclaimOfAnExpiredHolderSetsANewStartedAt(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 17, 0, 53, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	afterLapse := start.Add(releaseclaim.ReleaseVersionClaimTTL + time.Minute)
	holderB := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	sha, err := releaseclaim.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, afterLapse, nil)
	if err != nil {
		t.Fatalf("expected the abandoned claim to be reclaimed automatically, got refused: %v", err)
	}

	record, err := releaseclaim.ReadReleaseRepoClaimBlob(ctx, envB, sha)
	if err != nil {
		t.Fatalf("reading back the reclaiming holder's blob: %v", err)
	}
	if !record.StartedAt.Equal(afterLapse) {
		t.Errorf("reclaim StartedAt = %v, want the new holder's own claim time %v, not the abandoned holder's %v", record.StartedAt, afterLapse, start)
	}
}

func TestReleaseRepoClaimRefusalReportsHowLongTheHolderHasBeenRunning(t *testing.T) {
	envA := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, envA)
	runGitForTest(t, envA, "push", "-q", "-u", "origin", "main")
	envB := cloneRepoForTest(t, remote)

	start := time.Date(2026, 8, 29, 4, 0, 5, 0, time.UTC)
	ctx := newTestClaimContext()

	holderA := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-a", Tenant: "erun"}
	if _, err := releaseclaim.TakeReleaseRepoClaim(ctx, envA, "build-a", "1.0.220", holderA, start, nil); err != nil {
		t.Fatalf("first environment's claim: %v", err)
	}

	holderB := model.EnvironmentActivityLeaseHolder{Orchestrator: "orchestrator-b", Tenant: "erun"}
	_, err := releaseclaim.TakeReleaseRepoClaim(ctx, envB, "build-b", "1.0.220", holderB, start.Add(15*time.Minute), nil)
	if err == nil {
		t.Fatal("expected the second claim to be refused")
	}
	if !strings.Contains(err.Error(), "running for 15m0s") {
		t.Errorf("refusal must report how long the holder has been running, got: %v", err)
	}
}

func runGitCheckForTest(dir string, args ...string) error {
	cmd := core.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run()
}

// An older commit with the same subject outside the rebased range must be ignored.
func TestReleaseBranchPushRebaseIgnoresAnOlderCommitWithTheSameSubjectOutsideTheRebasedRange(t *testing.T) {
	repo := newAgentJobTestRepo(t)

	// A prior release with the same subject, already in history.
	if err := os.WriteFile(filepath.Join(repo, "OLD.txt"), []byte("old\n"), 0o644); err != nil {
		t.Fatalf("write old file: %v", err)
	}
	runGitForTest(t, repo, "add", "OLD.txt")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")

	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.213\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")
	runGitForTest(t, repo, "tag", "-a", "v1.0.213", "-m", "Release 1.0.213")
	runGitForTest(t, repo, "push", "-q", "origin", "v1.0.213")

	other := cloneRepoForTest(t, remote)
	if err := os.WriteFile(filepath.Join(other, "OTHER.txt"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file in the other checkout: %v", err)
	}
	runGitForTest(t, other, "add", "OTHER.txt")
	runGitForTest(t, other, "commit", "-q", "-m", "unrelated mainline change")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.214-next\n"), 0o644); err != nil {
		t.Fatalf("write VERSION bump: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] prepare 1.0.214-next")

	ctx := newTestClaimContext()
	spec := model.ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: model.ReleaseModeStable}
	command := model.ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	if err := releaseflow.RunReleaseBranchPush(ctx, spec, command, gitx.GitCommandRunner); err != nil {
		t.Fatalf("runReleaseBranchPush: %v", err)
	}

	runGitForTest(t, repo, "fetch", "-q", "origin", "main")
	if err := runGitCheckForTest(repo, "merge-base", "--is-ancestor", "v1.0.213", "FETCH_HEAD"); err != nil {
		t.Fatalf("expected v1.0.213 to remain reachable from origin/main after the rebase, but it does not: %v", err)
	}
}

// Two replayed commits with the tagged subject must refuse, not guess.
func TestReleaseBranchPushRebaseRefusesAnAmbiguousReplayedTagSubject(t *testing.T) {
	repo := newAgentJobTestRepo(t)
	remote := newBareRemoteForTest(t, repo)
	runGitForTest(t, repo, "push", "-q", "-u", "origin", "main")

	if err := os.WriteFile(filepath.Join(repo, "VERSION"), []byte("1.0.213\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	runGitForTest(t, repo, "add", "VERSION")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")
	runGitForTest(t, repo, "tag", "-a", "v1.0.213", "-m", "Release 1.0.213")
	runGitForTest(t, repo, "push", "-q", "origin", "v1.0.213")

	if err := os.WriteFile(filepath.Join(repo, "DUPLICATE.txt"), []byte("duplicate\n"), 0o644); err != nil {
		t.Fatalf("write duplicate-subject file: %v", err)
	}
	runGitForTest(t, repo, "add", "DUPLICATE.txt")
	runGitForTest(t, repo, "commit", "-q", "-m", "[skip ci] release 1.0.213")

	other := cloneRepoForTest(t, remote)
	if err := os.WriteFile(filepath.Join(other, "OTHER.txt"), []byte("unrelated change\n"), 0o644); err != nil {
		t.Fatalf("write unrelated file in the other checkout: %v", err)
	}
	runGitForTest(t, other, "add", "OTHER.txt")
	runGitForTest(t, other, "commit", "-q", "-m", "unrelated mainline change")
	runGitForTest(t, other, "push", "-q", "origin", "main")

	ctx := newTestClaimContext()
	spec := model.ReleaseSpec{ProjectRoot: repo, Branch: "main", Version: "1.0.213", Mode: model.ReleaseModeStable}
	command := model.ReleaseCommandSpec{Dir: repo, Name: "git", Args: []string{"push", "--follow-tags", "origin", "main"}}

	err := releaseflow.RunReleaseBranchPush(ctx, spec, command, gitx.GitCommandRunner)
	if err == nil {
		t.Fatal("expected an ambiguous replayed tag subject to refuse rather than silently retag")
	}
	if !strings.Contains(err.Error(), "v1.0.213") {
		t.Errorf("refusal must name the tag, got: %v", err)
	}
	if !strings.Contains(err.Error(), "release 1.0.213") {
		t.Errorf("refusal must name the subject, got: %v", err)
	}
	if !strings.Contains(err.Error(), "by hand") {
		t.Errorf("refusal must tell the operator to move the tag by hand, got: %v", err)
	}
}
