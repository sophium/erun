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
	// The auth message matches neither create_package nor scope-denied, so
	// the flow reaches the login-and-retry path.
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
		// Marker-tracked so the image is not reported as already published.
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
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "api user") printf 'octo-owner\n'; exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "git", "")
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh", "git", "helm")...)
	// tryGHCRLoginViaGH uses exec.LookPath("gh"), not ERUN_<NAME>_BIN.
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	// -vv locks the retry's `docker login` trace in the golden.
	result := erun.Run(t, []string{"build", "--release", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "build/real_run_release_push_auth_failure_retries_after_gh_login", normalize.Apply(result.Combined))
	// >= 2 pushes proves the retry happened.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read push counter: %v", err)
	}
	if pushes, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pushes < 2 {
		t.Fatalf("expected at least 2 docker push invocations (fail + retry), got %q", rawCount)
	}
}

func testBuildRealRunReleaseAbsorbsAConcurrentPublishersUnknownBlob(t *testing.T) {
	// The stub rejects the first push with "unknown blob" (a concurrent
	// publisher's uncommitted layer); the blob re-push must absorb it.
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
		// Marker-tracked so the image is not reported as already published.
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
	// tryGHCRLoginViaGH uses exec.LookPath("gh"), not ERUN_<NAME>_BIN.
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
	// >= 2 pushes proves the rejected push was re-run.
	rawCount, err := os.ReadFile(counter)
	if err != nil {
		t.Fatalf("read push counter: %v", err)
	}
	if pushes, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pushes < 2 {
		t.Fatalf("expected at least 2 docker push invocations (blob rejection + re-push), got %q", rawCount)
	}
}

func testBuildDryRunReleasePushesReleaseTaggedDockerBuilds(t *testing.T) {
	// The version-pinned "base" must still be pushed, not only tagged
	// (erun-devops/AGENTS.md "Wrapping And Pinning Third-Party Service Images").
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
