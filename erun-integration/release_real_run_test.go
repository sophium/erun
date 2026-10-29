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
	// The `==> Releasing`/`==> Released` traces appear only on real runs and drive the
	// desktop spinner. git is stubbed so mutations are deterministic no-ops.
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
	// Real-run --force replaces a stale local+remote tag, rewrites marketplace.json's
	// source.sha, and bumps chart and VERSION on disk.
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
	// Low headroom at the docker root refuses the release before the build; docker and df
	// are stubbed to report a low-space filesystem.
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
	// 1 GiB available, under the 20 GiB floor.
	fixture.StubBinaryAdvanced(t, stubs, "df", fixture.StubBinarySpec{
		Stdout: "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"overlay          104857600 93763584   1048576      99% " + dockerRoot + "\n",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "df")...)

	// The publish preflights live in `build --release`.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit for low disk headroom, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "only 1.0 GiB free at "+dockerRoot+", below the 20.0 GiB a multi-arch release build needs") {
		t.Fatalf("expected the disk-headroom refusal, naming the filesystem it measured, in output:\n%s", result.Combined)
	}
	// Docker stores were not measured, so the docker remedy must remain.
	if !strings.Contains(result.Combined, "free up space (docker system prune, remove unused images)") {
		t.Fatalf("expected the docker remediation a docker-root shortage can still use:\n%s", result.Combined)
	}
	if !strings.Contains(result.Combined, "filling this disk is what evicts the pod running the release") {
		t.Fatalf("expected the eviction-risk explanation in output:\n%s", result.Combined)
	}

	assertVersionFile(t, setup, "1.4.2\n")
}

func testReleaseRealRunRefusalDoesNotNameADockerRemedyDockerCannotPerform(t *testing.T) {
	// When the short filesystem is the home PVC and docker is measured holding nothing there,
	// docker prune is not a remedy and must not be offered.
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
	// 1 GiB available, under the 20 GiB floor.
	fixture.StubBinaryAdvanced(t, stubs, "df", fixture.StubBinarySpec{
		Stdout: "Filesystem     1024-blocks     Used Available Capacity Mounted on\n" +
			"overlay          104857600 93763584   1048576      99% " + dockerRoot + "\n",
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "df")...)

	// The publish preflights live in `build --release`.
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
	// The worktree-clean precondition (waived in dry-run) fails the run, and `==> Release failed`
	// must close the desktop lifecycle.
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
	// An interrupted earlier run left a local tag on a commit origin/main never saw; the
	// refusal names it as reclaimable and gives the remedy.
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
	// When the tag's commit is on origin/main, `git tag -d` must not be suggested.
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
