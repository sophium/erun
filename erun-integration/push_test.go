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

	t.Run("missing_version_errors", func(t *testing.T) {
		// push is a pure primitive: it publishes the version `erun build`
		// minted and never mints one, so a bare `erun push` with no version
		// argument must fail fast (root AGENTS.md § "Command primitives vs
		// orchestration"), mirroring deploy's version-required gate. The gate
		// fires before any docker call, so no stub is needed — and that is
		// exactly what keeps a bare push from shelling out to docker.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectDockerfile(t, setup)
		result := erun.Run(t, []string{"push", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for push without a version, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "push/missing_version_errors", normalize.Apply(result.Combined))
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
		// push builds from source, so the build's image push drives the retry
		// via runDockerBuildWithRetry: the first push fails auth,
		// ERUN_AUTO_LOGIN_ON_PUSH bypasses the prompt, and login + retry land it
		// (final exit 0). The auth failure is visible in the trace.
		result := erun.Run(t, []string{"push", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"devops", "container", "push", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/devops_container_push_real_run_resolves_single_image_spec", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_single_image_from_dockerfile_cwd", func(t *testing.T) {
		// Exercises the root `erun push` shorthand's single-image branch in
		// dry-run: with a Dockerfile in the cwd, ResolveDockerPushSpec
		// resolves one push spec and the dry-run trace must show the
		// would-run docker build/push commands without executing them.
		//
		// push builds from source, so even in dry-run the incremental
		// fingerprint check inspects the local image store (`docker image
		// inspect`) to decide promote-vs-rebuild — a sanctioned dry-run
		// decision input (erun-integration/AGENTS.md § "stubs as dry-run
		// decision input"). Stub docker so the inspect is deterministic and
		// needs no real daemon (the erun-devops image test stage runs this
		// gate with no docker on PATH); `inspect` exits 1 so no fp-tag is
		// present and the plan traces the rebuild path.
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
		result := erun.Run(t, []string{"push", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_single_image_from_dockerfile_cwd", normalize.Apply(result.Combined))
	})

	t.Run("real_run_single_image_from_dockerfile_cwd", func(t *testing.T) {
		// Exercises the root `erun push` single-image branch for real, and the
		// --version flag form of supplying the version (the positional form is
		// covered by the dry-run scenario above; both resolve through
		// resolvePushVersion). ResolveDockerPushSpec resolves the cwd Dockerfile
		// into one build+push pair, RunDockerPushSpec builds both platforms and
		// pushes the tag. The docker stub reports no cached fingerprint images
		// (image inspect exit 1) so the build path runs.
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
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_single_image_from_dockerfile_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_local_push_assembles_per_arch_manifest", func(t *testing.T) {
		// Locks per-arch manifest assembly for a multi-image `erun push`.
		// From the project root with no cwd Dockerfile, push resolves multiple
		// docker contexts (ResolveDockerPushExecution). A locally-built multi-arch
		// image only exists under per-arch tags, so push must push those and
		// assemble a manifest list — the same multi-platform push release uses —
		// not `docker push` the arch-less version tag (which would fail
		// "tag does not exist"). The stub answers every fp inspect present, so
		// images promote (mirroring the real failure); the plan must show, per
		// image, the per-arch `docker push` + `docker manifest create`/`push`.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"push", "1.0.0", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
	})

	t.Run("real_run_local_push_assembles_per_arch_manifest", func(t *testing.T) {
		// Companion to the dry-run scenario: drives RunDockerPushExecution +
		// pushMultiPlatformImage for real against the stub (promote, per-arch
		// docker push, docker manifest create/push). Previously this path failed
		// "tag does not exist" on the arch-less push; it must now complete.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"push", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
	})

	t.Run("real_run_auth_failure_prompts_login_and_retries", func(t *testing.T) {
		// Exercises promptDockerLoginRetry's interactive Select (the
		// non-ERUN_AUTO_LOGIN_ON_PUSH path) plus runDockerBuildWithRetry's
		// docker-login leg (push builds from source): the first image push
		// fails with a generic auth error,
		// "\r" confirms the highlighted "Login and retry push" option, the
		// stubbed `docker login` succeeds, and the retried push lands. The
		// Select is the run's single interactive prompt.
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
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"push", "1.0.0", "-v"}, erun.RunOptions{
			Cwd:   setup.Cwd,
			Env:   envVars,
			Stdin: "\r",
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_auth_failure_prompts_login_and_retries", normalize.Apply(result.Combined))
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
		result := erun.Run(t, []string{"push", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
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
		result := erun.Run(t, []string{"push", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_scope_denied_attempts_scope_refresh", normalize.Apply(result.Combined))
	})
}
