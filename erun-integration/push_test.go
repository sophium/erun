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

func TestPush(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		// Seed a Dockerfile in the test cwd so the root `erun push`
		// shorthand registers — without it, `push --help` falls through
		// to the root help and the push-specific flags (notably --force)
		// are not visible in the golden.
		setup := env.New(t)
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectDockerfile(t, setup)
		result := erun.Run(t, []string{"push", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/help", normalize.Apply(result.Combined))
	})

	t.Run("real_run_auth_failure_retries_after_login_via_auto_login_env", func(t *testing.T) {
		// Exercises runDockerPushWithRetry + promptDockerLoginRetry +
		// the docker-login retry path: a stubbed `docker push` exits
		// with an "access denied" auth-error message on the first call,
		// then succeeds on the retry. The new ERUN_AUTO_LOGIN_ON_PUSH
		// env var bypasses the interactive prompt so a non-TTY harness
		// can drive the retry. Asserts the final exit code is 0 and the
		// trace mentions the docker login that gates the retry.
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
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
		// Use `erun push` so runDockerPushWithRetry actually drives the
		// docker push. `erun build` alone does not push.
		result := erun.Run(t, []string{"push", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_auth_failure_retries_after_login_via_auto_login_env", normalize.Apply(result.Combined))
	})

	t.Run("devops_container_push_real_run_resolves_single_image_spec", func(t *testing.T) {
		// Exercises eruncommon.ResolveDockerPushSpec via the
		// `devops container push` nested command. With a Dockerfile in
		// the current directory and seeded devops chart context, the
		// command resolves a single-image push spec, runs the build,
		// and pushes the resolved image tag.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectDockerfile(t, setup)
		if err := os.WriteFile(filepath.Join(setup.Cwd, "VERSION"), []byte("1.0.0\n"), 0o644); err != nil {
			t.Fatalf("write VERSION: %v", err)
		}
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
			`case "$1" in`,
			`  image)`,
			`    case "$2" in inspect) exit 1 ;; *) exit 0 ;; esac ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"devops", "container", "push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/devops_container_push_real_run_resolves_single_image_spec", normalize.Apply(result.Combined))
	})

	t.Run("real_run_create_package_denied_emits_guidance", func(t *testing.T) {
		// Exercises handleNamespaceAuthError + retryAfterNamespaceLogin
		// + printCreatePackageGuidance + namespacePath + isGHCR for the
		// GHCR-specific "create_package" denial path. With `gh` absent
		// from PATH (the integration harness's PATH does not include the
		// dev's gh install at the stub-level), TryGHCRNamespaceLogin
		// returns false, the retry returns the auth error, and the user
		// gets the create-a-new-package guidance text. Asserts the
		// guidance markers appear and the command exits non-zero.
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
		// Stub `gh` to "exist but fail" so TryGHCRNamespaceLogin's
		// LookPath finds it, runs it, and falls through gracefully.
		fixture.StubBinaryAdvanced(t, stubs, "gh", fixture.StubBinarySpec{ExitCode: 1})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
		// Force PATH so `exec.LookPath("gh")` finds our stub (production
		// uses LookPath directly, not the ERUN_<NAME>_BIN override).
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
		result := erun.Run(t, []string{"push", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when create_package is denied, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "push/real_run_create_package_denied_emits_guidance", normalize.Apply(result.Combined))
	})

	t.Run("real_run_scope_denied_attempts_scope_refresh", func(t *testing.T) {
		// Exercises retryAfterScopeRefresh + RefreshGHCRPackageScopes
		// + scopeStillDenied: the docker push first fails with a
		// "does not match expected scopes" error (matches IsDockerScopeDenied
		// but NOT IsDockerCreatePackageDenied), the namespace login is
		// not applicable, then handleNamespaceAuthError calls
		// retryAfterScopeRefresh which runs `gh auth refresh`. The
		// stubbed gh exits 0 so RefreshGHCRPackageScopes returns true,
		// and the retry push succeeds on the second invocation.
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
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "gh")...)
		envVars = append(envVars, "PATH="+stubs+":"+os.Getenv("PATH"))
		envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
		result := erun.Run(t, []string{"push", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_scope_denied_attempts_scope_refresh", normalize.Apply(result.Combined))
	})
}
