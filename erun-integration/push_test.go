package integration

import (
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
		if !strings.Contains(result.Stdout, "--force") {
			t.Fatalf("expected push help to advertise --force, got:\n%s", result.Combined)
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
		if !strings.Contains(result.Combined, "docker login") {
			t.Errorf("expected retry trace to mention docker login, got:\n%s", result.Combined)
		}
	})
}
