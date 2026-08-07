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

func TestPush(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		// The root `erun push` shorthand only registers when a Dockerfile is
		// present in cwd; without the seed, `push --help` falls through to root
		// help and the push-specific flags never appear in the golden.
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
		// must fail fast (root AGENTS.md § "Command primitives vs
		// orchestration"). The gate fires before any docker call, so no stub
		// is needed.
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
		envVars = append(envVars, "ERUN_AUTO_LOGIN_ON_PUSH=1")
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_auth_failure_retries_after_login_via_auto_login_env", normalize.Apply(result.Combined))
	})

	t.Run("devops_container_push_real_run_resolves_single_image_spec", func(t *testing.T) {
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

	t.Run("dry_run_single_image_from_dockerfile_cwd", func(t *testing.T) {
		// Even in dry-run, push's incremental fingerprint check inspects the
		// local image store (`docker image inspect`) to decide
		// promote-vs-rebuild — a sanctioned dry-run decision input
		// (erun-integration/AGENTS.md § "stubs as dry-run decision input").
		// Stub docker for determinism and so no real daemon is needed (the
		// erun-devops image test stage runs this gate with no docker on PATH);
		// `inspect` exits 1 so nothing is cached and the plan traces rebuild.
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
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_single_image_from_dockerfile_cwd", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_build_shortcut_builds_then_pushes_minted_version", func(t *testing.T) {
		// `erun push --build` is the operator shortcut that builds the current
		// source first (minting a snapshot version) and pushes that exact
		// version, no --version needed.
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
		result := erun.Run(t, []string{"push", "--build", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_build_shortcut_builds_then_pushes_minted_version", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_build_shortcut_force_rebuilds", func(t *testing.T) {
		// --force must propagate to the --build step so the build rebuilds
		// every image instead of promoting from the fingerprint cache.
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
		result := erun.Run(t, []string{"push", "--build", "--force", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_build_shortcut_force_rebuilds", normalize.Apply(result.Combined))
	})

	t.Run("build_shortcut_with_explicit_version_errors", func(t *testing.T) {
		// --build mints the version itself, so combining it with an
		// explicit --version is contradictory and must fail clearly before any
		// build or push work.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedDevopsRepo(t, setup, "team", "dev")
		fixture.SeedProjectDockerfile(t, setup)
		result := erun.Run(t, []string{"push", "--build", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for --build with --version, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "push/build_shortcut_with_explicit_version_errors", normalize.Apply(result.Combined))
	})

	t.Run("real_run_single_image_from_dockerfile_cwd", func(t *testing.T) {
		// The docker stub reports no cached fingerprint images (image inspect
		// exit 1) so the real build+push path runs instead of promoting.
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
		// A locally-built multi-arch image only exists under per-arch tags, so
		// a multi-image push must push those and assemble a manifest list, not
		// `docker push` the arch-less version tag (which would fail "tag does
		// not exist"). The stub answers every fingerprint inspect as present so
		// images promote, mirroring the real failure this locks.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "--dry-run", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/dry_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
	})

	t.Run("real_run_local_push_assembles_per_arch_manifest", func(t *testing.T) {
		// Companion real-run to the dry-run manifest scenario: this path
		// previously failed "tag does not exist" on the arch-less push and must
		// now complete.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		fixture.StubBinary(t, stubs, "helm", "")
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_local_push_assembles_per_arch_manifest", normalize.Apply(result.Combined))
	})

	t.Run("real_run_transient_chart_verify_read_retries_then_verifies", func(t *testing.T) {
		// A release aborted mid-publish because the read-back of a chart erun had
		// just pushed answered 403 denied — the registry minted the pull token
		// before the new tag propagated. Verification reads an object erun itself
		// wrote, so it must retry with bounded backoff instead of failing the
		// publish. The helm stub fails the first `pull` with the exact ghcr error
		// and succeeds after, so the whole push must still complete.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		stubs := setup.Cwd + "/stubs"
		counter := setup.Cwd + "/helm-pull-counter"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`case "$1" in`,
			`  pull)`,
			`    count=0`,
			`    if [ -f '` + counter + `' ]; then count=$(cat '` + counter + `'); fi`,
			`    count=$((count + 1))`,
			`    printf '%s' "$count" > '` + counter + `'`,
			`    if [ "$count" = "1" ]; then`,
			`      printf 'Error: failed to perform "FetchReference" on source: response status code 403: denied: denied\n' >&2`,
			`      exit 1`,
			`    fi`,
			`    exit 0 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "push/real_run_transient_chart_verify_read_retries_then_verifies", normalize.Apply(result.Combined))
		// The pull count is a side effect outside the captured streams: 3 proves
		// the failed read really was retried (api fail + retry, then base) rather
		// than the failure being swallowed.
		rawCount, err := os.ReadFile(counter)
		if err != nil {
			t.Fatalf("read helm pull counter: %v", err)
		}
		if pulls, convErr := strconv.Atoi(strings.TrimSpace(string(rawCount))); convErr != nil || pulls != 3 {
			t.Fatalf("expected 3 helm pull invocations (fail + retry + second chart), got %q", rawCount)
		}
	})

	t.Run("real_run_persistent_chart_verify_read_reports_partial_publish", func(t *testing.T) {
		// The other half of the retry contract: a read that never succeeds must
		// still fail the push, and because the version's images are already public
		// by then, the failure must name exactly which charts landed, which did
		// not, and that re-running push is the recovery. The extra `zeta` chart
		// makes the middle chart fail so all three lists are non-empty.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		mustWriteFile(t, filepath.Join(setup.Cwd, "erun-devops", "k8s", "zeta", "Chart.yaml"),
			"apiVersion: v2\nname: zeta\nversion: 0.1.0\nappVersion: 0.1.0\n")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "docker", fixture.StubBinarySpec{ExitCode: 0})
		fixture.StubBinaryWithScript(t, stubs, "helm", strings.Join([]string{
			`case "$1" in`,
			`  pull)`,
			`    case "$2" in`,
			`      */charts/base)`,
			`        printf 'Error: failed to perform "FetchReference" on source: response status code 403: denied: denied\n' >&2`,
			`        exit 1 ;;`,
			`    esac`,
			`    exit 0 ;;`,
			`  *) exit 0 ;;`,
			`esac`,
		}, "\n"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v", "--environment", "local"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit for a chart that never verifies, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "push/real_run_persistent_chart_verify_read_reports_partial_publish", normalize.Apply(result.Combined))
	})

	t.Run("real_run_auth_failure_prompts_login_and_retries", func(t *testing.T) {
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
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker", "helm")...)
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{
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
		result := erun.Run(t, []string{"push", "--version", "1.0.0", "-v"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit when create_package is denied, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "push/real_run_create_package_denied_emits_guidance", normalize.Apply(result.Combined))
	})

	t.Run("real_run_scope_denied_attempts_scope_refresh", func(t *testing.T) {
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
	})

	t.Run("real_run_scope_denied_non_interactive_fails_clearly", func(t *testing.T) {
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
	})

	t.Run("real_run_scope_denied_in_pod_fails_clearly", func(t *testing.T) {
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
	})
}
