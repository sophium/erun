package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testReleaseRealRunEmitsReleaseLifecycleTraces(t *testing.T) {
	// Real-run (no --dry-run) candidate release. The `==> Releasing` /
	// `==> Released ... in <ELAPSED>` umbrella traces are emitted only on
	// a real run, so dry-run goldens cannot cover them; the desktop
	// activity queue keys off them to light the sidebar spinner for a
	// standalone `erun release`. git is stubbed so the tag is not skipped
	// and every mutation is a no-op, keeping output deterministic without
	// a real remote.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo develop ;;
  *'rev-parse --short HEAD'*) echo abc1234 ;;
  *'^{}'*) exit 1 ;;
  *) exit 0 ;;
esac
`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
	envVars = append(envVars, stubPublishToolchain(t, setup)...)
	result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/real_run_emits_release_lifecycle_traces", normalize.Apply(result.Combined))
}

func testReleaseRealRunForceStableSyncsMarketplaceAndBumpsVersion(t *testing.T) {
	// Real-run (no --dry-run) stable release with --force and a
	// marketplace.json. Three behaviors only a real run can prove:
	//   1. --force tag replacement runs the local `git tag -d` and remote
	//      `git push --delete` when the tag exists locally and on origin;
	//   2. the checksum-sync stage resolves the release commit and
	//      rewrites marketplace.json's source.sha on disk;
	//   3. the bump-stage file updates land: the chart gets the release
	//      version and VERSION gets the next patch.
	// git is stubbed so the tag probes report a stale tag and every
	// mutation is a no-op, keeping output deterministic without a real
	// remote. The on-disk rewrites are asserted directly because they are
	// side effects outside the captured streams.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	fixture.SeedMarketplaceJSON(t, setup.Cwd)
	fixture.RunGit(t, setup.Cwd, "add", ".claude-plugin")
	fixture.RunGit(t, setup.Cwd, "commit", "-m", "add marketplace.json")
	const releaseSHA = "feedfacefeedfacefeedfacefeedfacefeedface"
	envVars := append(setup.Env(), fixture.StubReleaseGit(t, setup.Cwd+"/stubs", fixture.ReleaseGitStubSpec{
		Branch:      "main",
		ShortCommit: "abc1234",
		TagSHA:      releaseSHA,
		RemoteTag:   "v1.4.2",
	})...)
	envVars = append(envVars, stubPublishToolchain(t, setup)...)

	result := erun.Run(t, []string{"release", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/real_run_force_stable_syncs_marketplace_and_bumps_version", normalize.Apply(result.Combined))

	marketplace, err := os.ReadFile(filepath.Join(setup.Cwd, ".claude-plugin", "marketplace.json"))
	if err != nil {
		t.Fatalf("read marketplace.json: %v", err)
	}
	if !strings.Contains(string(marketplace), `"sha": "`+releaseSHA+`"`) {
		t.Fatalf("marketplace.json source.sha not synced to release commit:\n%s", marketplace)
	}
	version, err := os.ReadFile(filepath.Join(setup.Cwd, "erun-devops", "VERSION"))
	if err != nil {
		t.Fatalf("read VERSION: %v", err)
	}
	if got := string(version); got != "1.4.3\n" {
		t.Fatalf("VERSION not bumped to next patch: %q", got)
	}
	chart, err := os.ReadFile(filepath.Join(setup.Cwd, "erun-devops", "k8s", "api", "Chart.yaml"))
	if err != nil {
		t.Fatalf("read Chart.yaml: %v", err)
	}
	if !strings.Contains(string(chart), "version: 1.4.2") {
		t.Fatalf("Chart.yaml not stamped with release version:\n%s", chart)
	}
}

func testReleaseRealRunRefusesWhenTheDockerRootIsNearlyFull(t *testing.T) {
	// The release that fills the node's disk is the one most likely
	// to get evicted by it, so low headroom at the docker root refuses the
	// release before the build spends anything — the same "known failure
	// caught up front" shape as the registry-permission preflight. docker
	// and df are both stubbed to report a real (if fake) low-space
	// filesystem, exercising the conclusive refusal branch rather than the
	// "not observable" fallback the other release scenarios take.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)
	dockerRoot := filepath.Join(setup.Cwd, "fake-docker-root")
	if err := os.MkdirAll(dockerRoot, 0o755); err != nil {
		t.Fatalf("mkdir fake docker root: %v", err)
	}
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1 $2" in`,
		`  "info -f") printf '%s' '` + dockerRoot + `' ;;`,
		`  "buildx prune") exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// 1 GiB available, well under the 20 GiB default floor.
	fixture.StubBinaryAdvanced(t, stubs, "df", fixture.StubBinarySpec{
		Stdout: "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"overlay          104857600 93763584   1048576      99% " + dockerRoot + "\n",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "df")...)

	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for low disk headroom, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "only 1.0 GiB free at "+dockerRoot+", below the 20.0 GiB a multi-arch release build needs") {
		t.Fatalf("expected the disk-headroom refusal, naming the filesystem it measured, in output:\n%s", result.Combined)
	}
	// docker's own stores were not measured here, so the remedy a docker-root
	// shortage can still use must survive.
	if !strings.Contains(result.Combined, "free up space (docker system prune, remove unused images)") {
		t.Fatalf("expected the docker remediation a docker-root shortage can still use:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "filling this disk is what evicts the pod running the release") {
		t.Fatalf("expected the eviction-risk explanation in output:\n%s", result.Combined)
	}

	// Outside the captured streams: the refusal must leave the version
	// file on the version it was releasing, so re-running retries it.
	assertVersionFile(t, setup, "1.4.2\n")
}

func testReleaseRealRunRefusalDoesNotNameADockerRemedyDockerCannotPerform(t *testing.T) {
	// The reported failure: the headroom that ran out was on the home PVC,
	// with every docker store on the node already empty — so the docker
	// remediation the refusal named could not free a byte of it, the number
	// did not move, and the real cause was never named. A docker prune is a
	// remedy only when docker was measured still holding space on the
	// filesystem that is short; here it is measured holding none, and the
	// only nonzero row is local volumes, which `docker system prune` does
	// not reclaim either.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)
	dockerRoot := filepath.Join(setup.Cwd, "fake-docker-root")
	if err := os.MkdirAll(dockerRoot, 0o755); err != nil {
		t.Fatalf("mkdir fake docker root: %v", err)
	}
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1 $2" in`,
		`  "info -f") printf '%s' '` + dockerRoot + `' ;;`,
		`  "system df") printf '%s\n' 'Images|0B' 'Containers|0B' 'Local Volumes|5.8GB (100%)' 'Build Cache|0B' ;;`,
		`  "buildx prune") exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// 1 GiB available, well under the 20 GiB default floor.
	fixture.StubBinaryAdvanced(t, stubs, "df", fixture.StubBinarySpec{
		Stdout: "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"overlay          104857600 93763584   1048576      99% " + dockerRoot + "\n",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "df")...)

	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for low disk headroom, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "only 1.0 GiB free at "+dockerRoot+", below the 20.0 GiB a multi-arch release build needs") {
		t.Fatalf("expected the disk-headroom refusal, naming the filesystem it measured, in output:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "docker's own stores have nothing left to reclaim there, so pruning docker cannot free any of it") {
		t.Fatalf("expected the refusal to say the docker remedy is spent, in output:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "the space this gate measured is held outside docker") {
		t.Fatalf("expected the refusal to name the space it measured, in output:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "docker system prune") {
		t.Fatalf("refusal names a docker remedy docker was measured unable to perform:\n%s", result.Combined)
	}
}

func testReleaseRealRunDirtyWorktreeFails(t *testing.T) {
	// Real-run with a modified tracked file: the worktree-clean
	// precondition (waived in dry-run so audits work anywhere) must
	// fail the run before any stage executes, and the `==> Release
	// failed after <ELAPSED>` umbrella trace must close the lifecycle
	// the desktop's activity queue opened on `==> Releasing`. Real git
	// throughout: the dirty state is a real uncommitted change.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "docker", "api", "Dockerfile"), "FROM alpine:3.23\n")

	result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for dirty worktree, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/real_run_dirty_worktree_fails", normalize.Apply(result.Combined))
}

func testReleaseRealRunUnpushedUnreachableStaleTagNamesTheLeftoverAndItsRemedy(t *testing.T) {
	// A previous release run tagged a commit and was interrupted
	// before it pushed anything (e.g. the pod holding the worktree was
	// replaced) — this run's own worktree has since moved past that
	// commit, so origin/main never saw it either. That is a safely
	// reclaimable leftover, not a real tag collision, so the refusal
	// names the diagnosis and the exact remedy instead of only "already
	// exists at <sha>, expected HEAD <sha>".
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "orphaned release stamp")
	fixture.RunGit(t, setup.Cwd, "tag", "v1.4.2")
	fixture.RunGit(t, setup.Cwd, "reset", "-q", "--hard", "HEAD~1")

	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for the stale tag, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, `a previous run left an unpushed local tag "v1.4.2"`) {
		t.Fatalf("expected the leftover-tag diagnosis in output:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "delete it with `git tag -d v1.4.2` to retry") {
		t.Fatalf("expected the exact remedy command in output:\n%s", result.Combined)
	}
}

func testReleaseRealRunATagCollisionReachableFromOriginGetsNoReclaimSuggestion(t *testing.T) {
	// The mirror case: the tag's commit already reached origin/main (a
	// genuine prior release, not a local leftover), so suggesting `git tag
	// -d` would offer to discard part of the published, agreed history.
	// The refusal must stay the plain "already exists" message.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "a real prior release")
	fixture.RunGit(t, setup.Cwd, "tag", "v1.4.2")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")
	fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-q", "-m", "unrelated local work")

	result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: releaseEnv(t, setup)})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for the real tag collision, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, `release tag "v1.4.2" already exists at`) {
		t.Fatalf("expected the plain already-exists refusal in output:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "delete it with") {
		t.Fatalf("must not suggest reclaiming a tag origin has already incorporated:\n%s", result.Combined)
	}
}
