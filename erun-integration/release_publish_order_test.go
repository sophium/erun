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
	// Regression. A release used to run only its git stages: it
	// tagged, announced, and bumped the version without ever building or
	// publishing an image or a chart, so the announced version was
	// undeployable and the gap only surfaced at the next deploy's chart
	// pull. Release now publishes between its local stages and its
	// outward-facing ones. Real git against a bare origin proves the
	// ordering behaviourally: the publish stage runs, the read-back
	// verification resolves each image and chart, and only then does the
	// tag reach origin. docker and helm are stubbed because the harness has
	// no daemon or registry; git is real so "public" means public.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)

	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
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
	// The other half of the same contract: when the publish cannot
	// complete, the release must fail while nothing is public. docker fails
	// every invocation, so the build inside the publish stage errors out.
	// The tag must not exist on origin and VERSION must still hold the
	// version that was being released, so re-running retries the same
	// version rather than stranding it behind a bump.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	seedBareOrigin(t, setup)
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{Stderr: "simulated docker failure", ExitCode: 1})
	fixture.StubBinary(t, stubs, "helm", "")

	// #1201: give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the simulated docker failure it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
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
	// Regression. A release established that it could fast-forward
	// once, in sync-remote, and then spent the whole build before pushing
	// anything; a base branch that moved in between was discovered at the
	// final push, with everything already public. The branch is now re-read
	// immediately before the build, and a release that cannot land refuses
	// while nothing is published and the version file is untouched.
	//
	// git is stubbed so the re-read reports a moved branch deterministically:
	// the move is what this scenario is about, and no fixture can make a real
	// remote move between two of the release's own git calls.
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

	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when the base branch moved before the build, got 0: %s", result.Combined)
	}
	golden.Equal(t, "release/real_run_refuses_when_the_base_branch_moved_before_the_build", normalize.Apply(result.Combined))

	// Outside the captured streams: the refusal must leave the version file
	// on the version it was releasing, so re-running retries that version.
	assertVersionFile(t, setup, "1.4.2\n")
}

func testReleaseRealRunAbsorbsABaseBranchThatMovedDuringTheBuild(t *testing.T) {
	// Regression, the other half. Releasing 1.0.176, a pull request
	// merged to main while the release was building: the images, the charts
	// and the tag all went public, and then the final push was rejected, so
	// the repository carried neither the packaging commit nor the version
	// bump and VERSION still read the version just published. The push now
	// rebases onto the moved branch and retries.
	//
	// The helm stub is what moves origin. helm first runs inside the publish
	// stage, which places the move exactly where the real one landed — after
	// the pre-build re-check, while the version is going public.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	origin := seedBareOrigin(t, setup)

	// The GitHub release entry is created when the linux packages are
	// attached, after the push. The gh stub's marker proves a rejected push
	// no longer strands it.
	githubReleaseCreated := filepath.Join(setup.Home, "github-release-created")
	mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli", "Dockerfile"), untestedLinuxPackageDockerfile)
	fixture.RunGit(t, setup.Cwd, "add", ".")
	fixture.RunGit(t, setup.Cwd, "commit", "-q", "-m", "add linux package")
	fixture.RunGit(t, setup.Cwd, "push", "-q", "origin", "main")

	// The checkout that stands in for the pull request, cloned once origin is
	// at the commit the release starts from. -b main because the bare origin's
	// HEAD still names whatever branch git init defaults to, so a plain clone
	// would land on an unborn branch with no main to push.
	merging := filepath.Join(setup.Home, "merging")
	fixture.RunGit(t, setup.Home, "clone", "-q", "-b", "main", origin, merging)
	fixture.RunGit(t, merging, "config", "user.email", "test@example")
	fixture.RunGit(t, merging, "config", "user.name", "Test")

	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDockerWithManifestTracking(t, stubs)
	fixture.StubBinaryMergingIntoRemoteOnce(t, stubs, "helm", merging, "main", "a merged pull request")
	// The release does not exist yet, so gh views nothing and creates it.
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "release view") exit 1 ;;`,
		`  "release create") : > '` + filepath.ToSlash(githubReleaseCreated) + `' ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm", "gh")...)
	envVars = append(envVars,
		// #1201: give the registry-credential preflight a resolvable credential
		// so this scenario still reaches the moved-branch-absorption it is about.
		"GH_TOKEN=integration-test-token",
	)

	// release marks source control only, so the publish and the preflights it
	// owns now live in `build --release`: this asserts them where they moved to
	// rather than dropping the coverage.
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	// git's push-rejection hint block is worded differently across git
	// versions, so it cannot be part of a reviewed golden; the `! [rejected]`
	// line and erun's own retry line are what this scenario locks.
	combined := normalize.Apply(result.Combined, normalize.Replacement{Pattern: regexp.MustCompile(`(?m)^hint:.*\n?`), Token: ""})
	golden.Equal(t, "release/real_run_absorbs_a_base_branch_that_moved_during_the_build", combined)

	// Side effects outside the captured streams, one per contract the
	// absorbed push has to hold: the repository records the release it
	// published, the tag is public, the version moved past what was
	// published, and the GitHub release step still ran.
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
	// The misdiagnosed direction of the release push retry. Releasing 1.0.258, main pushed
	// and develop was rejected as a non-fast-forward: another release had
	// advanced origin/develop while this release's own sync-develop added
	// commits locally. The retry attributed the rejection to origin/main —
	// which had not moved — and rebased it twice, a no-op each time, never
	// fetching or merging origin/develop.
	//
	// The scenario pins the shape rather than the wording: origin/develop
	// carries a commit this checkout's develop never will, so the develop
	// ref is genuinely diverged when the push runs while main is cleanly
	// ahead of origin/main.
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

	// Everything the release publishes landed before the develop rejection:
	// main carries the release and the prepare commit, and the tag is public.
	// develop is what did not land, and the failure has to say so.
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
	// Regression for erun#1619: two orchestrators driving the same runtime
	// pod could both pass every preflight and race the same release, the
	// loser discovering the collision only via a tag mismatch after minutes
	// of wasted publishing. Pre-claiming the exact exclusive lease scope
	// release itself takes — via the activity lease CLI, sharing this
	// scenario's XDG_CACHE_HOME — simulates "another orchestrator got there
	// first". release must refuse before it ever touches git — sync-remote
	// never runs — though execution-plan resolution has already made its
	// own read-only docker fingerprint check by this point, hence the
	// docker stub.
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

	// Outside the captured streams: the refusal must leave the version file
	// untouched, so re-running retries once the other release finishes (or
	// its claim is reclaimed automatically).
	assertVersionFile(t, setup, "1.4.2\n")
}
