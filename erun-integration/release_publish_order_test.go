package integration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func testReleaseRealRunPublishesBeforeTheTagReachesOrigin(t *testing.T) {
	// The tag reaches origin only after publish and read-back verification. git is real
	// (bare origin) so "public" means public; docker and helm are stubbed.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)

	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubPublishToolchain(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "release/real_run_publishes_before_the_tag_reaches_origin", normalize.Apply(result.Combined))

	if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
		t.Fatalf("release tag did not reach origin after a successful publish:\n%s", tags)
	}
	assertVersionFile(t, setup, "1.4.3\n")
}

func testReleaseRealRunPublishFailureLeavesNoPublicTag(t *testing.T) {
	// A failed publish leaves no tag on origin and VERSION unchanged, so a re-run retries it.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{Stderr: "simulated docker failure", ExitCode: 1})
	fixture.StubBinary(t, stubs, "helm", "")

	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the publish fails, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/real_run_publish_failure_leaves_no_public_tag", normalize.Apply(result.Combined))

	if tags := remoteTags(t, setup); strings.Contains(tags, "refs/tags/v1.4.2") {
		t.Fatalf("a failed release pushed its tag to origin:\n%s", tags)
	}
	assertVersionFile(t, setup, "1.4.2\n")
}

func testReleaseRealRunRefusesWhenTheBaseBranchMovedBeforeTheBuild(t *testing.T) {
	// The base branch is re-read right before the build; git is stubbed because no fixture
	// can move a real remote between two of the release's own git calls.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "git", `case "$*" in
  *'rev-parse --abbrev-ref HEAD'*) echo main ;;
  *'rev-list --count HEAD..FETCH_HEAD'*) echo 3 ;;
  *'rev-parse --short HEAD'*) echo abc1234 ;;
  *'show-ref --verify --quiet'*) exit 1 ;;
  *'^{}'*) exit 1 ;;
  *) : ;;
esac
exit 0
`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "git")...)
	envVars = append(envVars, stubPublishToolchain(t, setup)...)

	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the base branch moved before the build, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/real_run_refuses_when_the_base_branch_moved_before_the_build", normalize.Apply(result.Combined))

	assertVersionFile(t, setup, "1.4.2\n")
}

func testReleaseRealRunAbsorbsABaseBranchThatMovedDuringTheBuild(t *testing.T) {
	// A base branch moving after publish must not strand the bump: the final push rebases
	// and retries. helm's first call (inside publish) moves origin at the realistic point.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	origin := seedBareOrigin(t, setup)

	// The gh stub's marker proves a rejected push does not strand the GitHub release.
	githubReleaseCreated := filepath.Join(setup.Home, "github-release-created")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli", "Dockerfile"), untestedLinuxPackageDockerfile)
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add linux package")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

	// -b main: the bare origin's HEAD names git init's default branch, which may be unborn.
	merging := filepath.Join(setup.Home, "merging")
	fixture.RunGit(t, setup.Home, "clone", "-q", "-b", "main", origin, merging)
	fixture.RunGit(t, merging, "config", "user.email", "test@example")
	fixture.RunGit(t, merging, "config", "user.name", "Test")

	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDockerWithManifestTracking(t, stubs)
	fixture.StubBinaryMergingIntoRemoteOnce(t, stubs, "helm", merging, "main", "a merged pull request")
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "release view") exit 1 ;;`,
		`  "release create") : > '` + filepath.ToSlash(githubReleaseCreated) + `' ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm", "gh")...)
	envVars = append(envVars,
		"GH_TOKEN=integration-test-token",
	)

	// The publish lives in `build --release`; release only marks source control.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// git's hint block wording varies by version, so it is stripped from the golden.
	combined := normalize.Apply(result.Combined, normalize.Replacement{Pattern: regexp.MustCompile(`(?m)^hint:.*\n?`), Token: ""})
	golden.Equal(t, "release/real_run_absorbs_a_base_branch_that_moved_during_the_build", combined)

	remote := remoteMainSubjects(t, merging)
	for _, want := range []string{"a merged pull request", "[skip ci] release 1.4.2", "[skip ci] prepare 1.4.3"} {
		if !strings.Contains(remote, want) {
			t.Fatalf("origin/main is missing %q after the absorbed push:\n%s", want, remote)
		}
	}
	if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
		t.Fatalf("release tag did not reach origin after the absorbed push:\n%s", tags)
	}
	assertVersionFile(t, setup, "1.4.3\n")
	if _, err := os.Stat(githubReleaseCreated); err != nil {
		t.Fatalf("the linux packages were never attached, so the GitHub release entry was stranded: %v", err)
	}
}

func testReleaseRealRunNamesTheRejectedDevelopAndDoesNotRebaseMain(t *testing.T) {
	// The rejected ref is develop (diverged on origin), while main is cleanly ahead; the
	// retry must not blame main.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	origin := seedBareOrigin(t, setup)

	fixture.RunGit(t, setup.Cwd, "branch", "develop")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "develop")

	diverging := filepath.Join(setup.Home, "diverging")
	fixture.RunGit(t, setup.Home, "clone", "-q", "-b", "develop", origin, diverging)
	fixture.RunGit(t, diverging, "config", "user.email", "test@example")
	fixture.RunGit(t, diverging, "config", "user.name", "Test")
	mustWriteFile(t, filepath.Join(diverging, "another-release.txt"), "another release\n")
	fixture.RunGit(t, diverging, "add", ".")
	fixture.RunGit(t, diverging, "commit", "-q", "-m", "another release advanced develop")
	fixture.RunGit(t, diverging, "push", "-q", "origin", "develop")

	envVars := append(setup.Env(), stubPublishToolchain(t, setup)...)
	envVars = append(envVars, "ERUN_HOST_OS_OVERRIDE=linux")

	result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when develop is rejected, got 0: %s", result.Combined)
	}
	if strings.Contains(result.Combined, "origin/main moved during the release") {
		t.Fatalf("a develop rejection must not be reported as origin/main having moved:\n%s", result.Combined)
	}
	combined := normalize.Apply(result.Combined, normalize.Replacement{Pattern: regexp.MustCompile(`(?m)^hint:.*\n?`), Token: ""})
	golden.Equal(t, "release/real_run_names_the_rejected_develop_and_does_not_rebase_main", combined)

	// main and the tag landed before develop was rejected; the failure must say so.
	remote := remoteMainSubjects(t, diverging)
	for _, want := range []string{"[skip ci] release 1.4.2", "[skip ci] prepare 1.4.3"} {
		if !strings.Contains(remote, want) {
			t.Fatalf("origin/main is missing %q before the develop rejection:\n%s", want, remote)
		}
	}
	if tags := remoteTags(t, setup); !strings.Contains(tags, "refs/tags/v1.4.2") {
		t.Fatalf("the release tag should already be public when develop is rejected:\n%s", tags)
	}
}

func testReleaseRealRunRefusesWhenAnotherOrchestratorIsAlreadyReleasingTheVersion(t *testing.T) {
	// A pre-claimed exclusive lease (shared XDG_CACHE_HOME) makes release refuse before
	// touching git; the docker stub covers the fingerprint read made during plan resolution.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")

	blocker := erun.Run(t, []string{
		"activity", "lease", "take", "--tenant", "erun", "--environment", "build",
		"--name", "blocker", "--exclusive", "--scope", "release-version:1.4.2",
		"--orchestrator", "existing-holder",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: inEnvironment(setup.Env())})
	if blocker.ExitCode != 0 {
		t.Fatalf("pre-claim: exit %d: %s", blocker.ExitCode, blocker.Combined)
	}

	envVars := append(releaseEnv(t, setup), "ERUN_TENANT=erun", "ERUN_ENVIRONMENT=build")
	result := erun.Run(t, []string{"release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the version is already being released, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "1.4.2 is already being released by orchestrator existing-holder") {
		t.Fatalf("expected the refusal to name the version and the holder:\n%s", result.Combined)
	}

	assertVersionFile(t, setup, "1.4.2\n")
}
