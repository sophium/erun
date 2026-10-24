package integration

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func buildReleasePushScenarios(t *testing.T) {
	t.Run("real_run_release_push_auth_failure_retries_after_gh_login", testBuildRealRunReleasePushAuthFailureRetriesAfterGhLogin)
	t.Run("real_run_release_absorbs_a_concurrent_publishers_unknown_blob", testBuildRealRunReleaseAbsorbsAConcurrentPublishersUnknownBlob)
	t.Run("dry_run_release_pushes_release_tagged_docker_builds", testBuildDryRunReleasePushesReleaseTaggedDockerBuilds)
}

func testBuildRealRunReleasePushAuthFailureRetriesAfterGhLogin(t *testing.T) {
	// A denied release push drives the build-side GHCR auth-retry chain:
	// ERUN_AUTO_LOGIN_ON_PUSH auto-confirms the login retry, GHCR re-auth
	// resolves user + token from the gh stub, and the whole image build retries
	// until the pushes succeed. The auth message deliberately matches neither
	// create_package nor scope-denied so the flow falls through to the
	// login-and-retry path rather than a namespace-specific error.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	stubs := setup.Cwd + "/stubs"
	counter := filepath.Join(stubs, "docker-push-counter")
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    count=0`,
		`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`    count=$((count + 1))`,
		`    printf '%s' "$count" > '` + counter + `'`,
		`    if [ "$count" = "1" ]; then`,
		`      printf 'unauthorized: authentication required: denied: requested access to the resource is denied\n' >&2`,
		`      exit 1`,
		`    fi`,
		`    exit 0 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		// manifest inspect backs both the pre-publish probe and the
		// post-publish verify; marker-file-tracked so this scenario does
		// not falsely report the image as already published before
		// manifest push has run.
		`  manifest)`,
		`    marker="` + stubs + `/manifest-published-$(printf '%s' "$3" | tr '/:' '__')"`,
		`    case "$2" in`,
		`      inspect) [ -f "$marker" ] && exit 0 || exit 1 ;;`,
		`      push) touch "$marker" ; exit 0 ;;`,
		`      *) exit 0 ;;`,
		`    esac`,
		`    ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// The gh stub answers the user lookup and token read the GHCR login
	// performs; docker's `login --password-stdin` lands in the stub's default
	// exit-0 arm.
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "api user") printf 'octo-owner\n'; exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// Release operations (tag, push) go through the git stub so the
	// release stage succeeds without a real remote.
	fixture.StubBinary(t, stubs, "git", "")
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh", "git", "helm")...)
	// tryGHCRLoginViaGH gates on exec.LookPath("gh"), which reads PATH
	// rather than the ERUN_<NAME>_BIN override.
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	// -vv so the `docker login ghcr.io` TraceCommand that gates the
	// retry is locked in the golden; at lower verbosity the retry is
	// only provable by the exit code.
	result := erun.Run(t, []string{"build", "--release", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_release_push_auth_failure_retries_after_gh_login", normalize.Apply(result.Combined))
	// The push counter is a side effect outside the captured streams:
	// >= 2 proves the failed push really was retried after the login
	// rather than the auth error being swallowed.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read push counter: %v", err)
	}
	if pushes, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pushes < 2 {
		t.Fatalf("expected at least 2 docker push invocations (fail + retry), got %q", rawCount)
	}
}

func testBuildRealRunReleaseAbsorbsAConcurrentPublishersUnknownBlob(t *testing.T) {
	// A release whose push is rejected because a concurrent publisher's
	// upload of a shared layer has not committed yet must re-push and finish,
	// not fail the build four minutes in with everything already built.
	// The docker stub rejects the first push with "unknown blob" and accepts
	// every later one — the observable shape the registry-side race takes for
	// the run that loses it.
	//
	// The gh stub exists for the credential preflight GHCR runs before any
	// build (an anonymous push is refused up front), not to drive a login
	// retry: "unknown blob" is not an authorization failure, so the retry
	// that absorbs it is the push funnel's blob re-push, which the
	// "re-pushing" assertion below pins. ERUN_AUTO_LOGIN_ON_PUSH only keeps
	// an unexpected login path from blocking on a prompt.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	counter := filepath.Join(stubs, "docker-push-counter")
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    count=0`,
		`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`    count=$((count + 1))`,
		`    printf '%s' "$count" > '` + counter + `'`,
		`    if [ "$count" = "1" ]; then`,
		`      printf 'unknown blob\n' >&2`,
		`      exit 1`,
		`    fi`,
		`    exit 0 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		// manifest inspect backs both the pre-publish probe and the
		// post-publish verify; marker-file-tracked so this scenario does
		// not falsely report the image as already published before
		// manifest push has run.
		`  manifest)`,
		`    marker="` + stubs + `/manifest-published-$(printf '%s' "$3" | tr '/:' '__')"`,
		`    case "$2" in`,
		`      inspect) [ -f "$marker" ] && exit 0 || exit 1 ;;`,
		`      push) touch "$marker" ; exit 0 ;;`,
		`      *) exit 0 ;;`,
		`    esac`,
		`    ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// Release operations (tag, push) go through the git stub so the release
	// stage succeeds without a real remote. The gh stub answers the user
	// lookup and token read the GHCR credential preflight performs.
	fixture.StubBinary(t, stubs, "git", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "api user") printf 'octo-owner\n'; exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh", "git", "helm")...)
	// tryGHCRLoginViaGH gates on exec.LookPath("gh"), which reads PATH rather
	// than the ERUN_<NAME>_BIN override.
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	result := erun.Run(t, []string{"build", "--release"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_release_absorbs_a_concurrent_publishers_unknown_blob", normalize.Apply(result.Combined))
	if !strings.Contains(result.Combined, "re-pushing (1/2)") {
		t.Errorf("expected the release to report the bounded re-push rather than failing, got:\n%s", result.Combined)
	}
	// The push counter is a side effect outside the captured streams: >= 2
	// proves the rejected push was really re-run instead of the rejection
	// being swallowed.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read push counter: %v", err)
	}
	if pushes, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pushes < 2 {
		t.Fatalf("expected at least 2 docker push invocations (blob rejection + re-push), got %q", rawCount)
	}
}

func testBuildDryRunReleasePushesReleaseTaggedDockerBuilds(t *testing.T) {
	// --release dry-run must trace the per-platform docker build + docker push
	// for the release-tagged image, plus the local tag for downstream
	// dependencies. "base" (fixture.SeedReleaseRepo) is a version-pinned
	// wrapper carrying its own VERSION rather than the release's: a release
	// must still publish it, not only build and locally tag it, or a
	// component whose chart is published never gets an image the registry
	// actually has (erun-oci-registry's own defect, see erun-devops/AGENTS.md
	// "Wrapping And Pinning Third-Party Service Images").
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "main")
	result := erun.Run(t, []string{"build", "--release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), stubDockerNoLocalImages(t, setup)...)})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "docker manifest push ghcr.io/sophium/base:") {
		t.Fatalf("expected the version-pinned wrapper image \"base\" to be pushed and assembled into a manifest, not only built and locally tagged: %s", result.Combined)
	}
	golden.Equal(t, "build/dry_run_release_pushes_release_tagged_docker_builds", normalize.Apply(result.Combined))
}
