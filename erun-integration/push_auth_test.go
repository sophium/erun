package integration

import (
	"os"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func pushRegistryAuthScenarios(t *testing.T) {
	t.Run("real_run_refuses_before_build_when_no_registry_credential_configured", testPushRealRunRefusesBeforeBuildWhenNoRegistryCredentialConfigured)
	t.Run("real_run_auth_failure_retries_after_login_via_auto_login_env", testPushRealRunAuthFailureRetriesAfterLoginViaAutoLoginEnv)
	t.Run("real_run_auth_failure_prompts_login_and_retries", testPushRealRunAuthFailurePromptsLoginAndRetries)
	t.Run("real_run_create_package_denied_emits_guidance", testPushRealRunCreatePackageDeniedEmitsGuidance)
	t.Run("real_run_scope_denied_attempts_scope_refresh", testPushRealRunScopeDeniedAttemptsScopeRefresh)
	t.Run("real_run_scope_denied_non_interactive_fails_clearly", testPushRealRunScopeDeniedNonInteractiveFailsClearly)
	t.Run("real_run_scope_denied_in_pod_fails_clearly", testPushRealRunScopeDeniedInPodFailsClearly)
}

func testPushRealRunRefusesBeforeBuildWhenNoRegistryCredentialConfigured(t *testing.T) {
	// #1201: a pod that has never authenticated to ghcr.io (no docker config,
	// no gh session, no GH_TOKEN) used to discover that only at the real push,
	// after a full multi-arch build. `docker image inspect` is stubbed only
	// because fingerprint resolution needs an answer before the push plan can
	// even be built; every other docker verb (build/push/manifest/login) is
	// deliberately left to print a loud marker and fail, so a regression that
	// let the flow reach an actual build or push shows up unmistakably in the
	// golden instead of silently succeeding.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *)`,
		`    echo "UNEXPECTED DOCKER CALL: $*" >&2`,
		`    exit 1 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"push", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when no ghcr.io credential is configured, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_refuses_before_build_when_no_registry_credential_configured", normalize.Apply(result.Combined))
}

func testPushRealRunAuthFailureRetriesAfterLoginViaAutoLoginEnv(t *testing.T) {
	// push builds from source, so the build's image push is what hits the
	// auth failure. ERUN_AUTO_LOGIN_ON_PUSH bypasses the interactive login
	// prompt so this non-TTY harness can drive the failure-then-retry path.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	counter := setup.Cwd + "/docker-push-counter"
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
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	// #1201: the registry-credential preflight refuses up front when no
	// ghcr.io credential resolves at all; GH_TOKEN gives it one so this
	// scenario still reaches the auth-failure-at-push it is about.
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1", "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_auth_failure_retries_after_login_via_auto_login_env", normalize.Apply(result.Combined))
}

func testPushRealRunAuthFailurePromptsLoginAndRetries(t *testing.T) {
	// The interactive login-retry path (no ERUN_AUTO_LOGIN_ON_PUSH): the
	// first push fails auth, "\r" confirms the "Login and retry push"
	// select, and the retried push lands. That select is the run's single
	// interactive prompt.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	counter := setup.Cwd + "/docker-push-counter"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    count=0`,
		`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`    count=$((count + 1))`,
		`    printf '%s' "$count" > '` + counter + `'`,
		`    if [ "$count" = "1" ]; then`,
		`      printf 'unauthorized: authentication required\n' >&2`,
		`      exit 1`,
		`    fi`,
		`    exit 0 ;;`,
		`  login) exit 0 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	// #1201: give the registry-credential preflight a resolvable credential
	// so this scenario still reaches the interactive login-retry it is about.
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{
		Cwd:   setup.Cwd,
		Env:   envVars,
		Stdin: "\r",
	})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_auth_failure_prompts_login_and_retries", normalize.Apply(result.Combined))
}

func testPushRealRunCreatePackageDeniedEmitsGuidance(t *testing.T) {
	// GHCR "create_package" denial: when the namespace login cannot grant
	// access, the user gets the create-a-new-package guidance and a
	// non-zero exit.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    printf 'denied: denied: create_package permission_denied\n' >&2`,
		`    exit 1 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// Stub gh to "exist but fail" so LookPath finds it, runs it, and falls
	// through gracefully instead of taking the gh-absent branch.
	fixture.StubBinaryAdvanced(t, stubs, "gh", fixture.StubBinarySpec{ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
	// Force PATH so `exec.LookPath("gh")` finds our stub (production
	// uses LookPath directly, not the ERUN_<NAME>_BIN override).
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	// #1201: GH_TOKEN gives the registry-credential preflight a resolvable
	// credential without changing the failing gh stub the login-retry
	// behavior above still depends on.
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when create_package is denied, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_create_package_denied_emits_guidance", normalize.Apply(result.Combined))
}

func testPushRealRunScopeDeniedAttemptsScopeRefresh(t *testing.T) {
	// The scope-denied path (distinct from create_package: the error
	// matches IsDockerScopeDenied, not IsDockerCreatePackageDenied): scope
	// refresh via `gh auth refresh` succeeds and the retry push lands.
	//
	// ERUN_FORCE_TTY=1 is the deliberate seam that makes this piped-stdin
	// run count as interactive so the interactive scope-refresh success
	// path stays covered; the two scenarios below assert the
	// non-interactive and in-pod paths fail clearly instead of launching gh.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	counter := setup.Cwd + "/docker-push-counter"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    count=0`,
		`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
		`    count=$((count + 1))`,
		`    printf '%s' "$count" > '` + counter + `'`,
		// Push #1: original failure that triggers handleNamespaceAuthError.
		// Push #2: namespace-login retry path also fails so scopeStillDenied
		//   stays true and retryAfterScopeRefresh runs.
		// Push #3+: succeed (after gh auth refresh).
		`    if [ "$count" -le "2" ]; then`,
		`      printf 'denied: token does not match expected scopes\n' >&2`,
		`      exit 1`,
		`    fi`,
		`    exit 0 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// Stub gh: auth switch fails (so RefreshGHCRPackageScopes uses
	// the auth-refresh path against the active account), auth
	// refresh succeeds, auth token returns a non-empty token so
	// TryGHCRNamespaceLogin's docker-login attempt runs.
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "auth switch") exit 1 ;;`,
		`  "auth refresh") exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinary(t, stubs, "helm", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh", "helm")...)
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1", "ERUN_FORCE_TTY=1")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_scope_denied_attempts_scope_refresh", normalize.Apply(result.Combined))
}

func testPushRealRunScopeDeniedNonInteractiveFailsClearly(t *testing.T) {
	// With no interactive terminal (piped stdin, ERUN_FORCE_TTY unset),
	// RefreshGHCRPackageScopes must NOT launch gh's interactive device-code
	// flow — which would hang forever — and must instead return the
	// actionable write:packages-scope error.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	marker := setup.Cwd + "/gh-interactive-marker"
	// docker push always fails the scope check, so the only way the push
	// could succeed is the (gated) interactive scope refresh.
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    printf 'denied: token does not match expected scopes\n' >&2`,
		`    exit 1 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	// gh: token works (so the namespace-login retry runs), but any
	// interactive auth call writes the marker — it must never fire.
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "auth switch"|"auth refresh"|"auth login") printf 'launched\n' > '` + marker + `'; exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when scope refresh is gated, got 0:\n%s", result.Combined)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("interactive gh auth flow was launched in a non-interactive context:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_scope_denied_non_interactive_fails_clearly", normalize.Apply(result.Combined))
}

func testPushRealRunScopeDeniedInPodFailsClearly(t *testing.T) {
	// inside a chart-injected runtime pod (ERUN_TENANT/
	// ERUN_ENVIRONMENT set) the desktop terminal is a PTY-backed pod
	// shell, so ERUN_FORCE_TTY=1 is set to prove the in-pod check wins
	// over the TTY seam: even with a "terminal", there is no browser, so
	// the gate must still fire and the interactive gh flow must not run.
	setup := env.New(t)
	fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
	stubs := setup.Cwd + "/stubs"
	marker := setup.Cwd + "/gh-interactive-marker"
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$1" in`,
		`  push)`,
		`    printf 'denied: token does not match expected scopes\n' >&2`,
		`    exit 1 ;;`,
		`  image)`,
		`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	fixture.StubBinaryWithScript(t, stubs, "gh", strings.Join([]string{
		`case "$1 $2" in`,
		`  "auth switch"|"auth refresh"|"auth login") printf 'launched\n' > '` + marker + `'; exit 0 ;;`,
		`  "auth token") printf 'gh-token\n'; exit 0 ;;`,
		`  *) exit 0 ;;`,
		`esac`,
	}, "\n"))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1", "ERUN_FORCE_TTY=1")
	envVars = append(envVars, "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when scope refresh is gated in-pod, got 0:\n%s", result.Combined)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatalf("interactive gh auth flow was launched inside the runtime pod:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_scope_denied_in_pod_fails_clearly", normalize.Apply(result.Combined))
}
