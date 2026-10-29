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
	// Only `docker image inspect` is stubbed (fingerprinting needs it); every other docker verb
	// fails loudly so reaching a build or push shows in the golden.
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
	// ERUN_AUTO_LOGIN_ON_PUSH bypasses the login prompt for this non-TTY harness.
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
	// GH_TOKEN gets past the credential preflight to the auth failure at push.
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1", "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "push/real_run_auth_failure_retries_after_login_via_auto_login_env", normalize.Apply(result.Combined))
}

func testPushRealRunAuthFailurePromptsLoginAndRetries(t *testing.T) {
	// "\r" confirms the run's single prompt, "Login and retry push".
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
	// gh exists but fails, so the flow falls through instead of taking the gh-absent branch.
	fixture.StubBinaryAdvanced(t, stubs, "gh", fixture.StubBinarySpec{ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
	// Production resolves gh via LookPath, not ERUN_GH_BIN.
	envVars = append(envVars, "PATH="+stubs+string(os.PathListSeparator)+setup.PathDir)
	envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
	// GH_TOKEN passes the preflight without changing the failing gh stub.
	envVars = append(envVars, "GH_TOKEN=integration-test-token")
	result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit when create_package is denied, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "push/real_run_create_package_denied_emits_guidance", normalize.Apply(result.Combined))
}

func testPushRealRunScopeDeniedAttemptsScopeRefresh(t *testing.T) {
	// Scope-denied (not create_package): `gh auth refresh` succeeds and the retry lands.
	// ERUN_FORCE_TTY=1 makes piped stdin count as interactive.
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
		// Pushes 1-2 fail so retryAfterScopeRefresh runs; later pushes succeed.
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
	// auth switch fails (forcing auth refresh); auth token is non-empty so docker login runs.
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
	// Without a TTY, gh's device-code flow would hang; the scope error is returned instead.
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
	// Any interactive gh auth call writes the marker; it must never fire.
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
	// In a runtime pod there is no browser, so the gate fires even with ERUN_FORCE_TTY=1.
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
