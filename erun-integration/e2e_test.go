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

// e2eValidSuiteConfig is a playwright.config.ts that satisfies erun e2e's
// lint: baseURL comes from the injected env var, never a literal, and
// TLS verification is never disabled.
const e2eValidSuiteConfig = "export default {\n  use: {\n    baseURL: process.env.ERUN_E2E_BASE_URL,\n  },\n};\n"

// seedPlaywrightSuite writes a playwright.config.ts at
// <projectRoot>/<devopsDir>/playwright[/<component>], the same
// "<tenant>-devops/playwright" convention discovery already uses for
// docker/ and k8s/.
func seedPlaywrightSuite(t testing.TB, projectRoot, devopsDir, component, configBody string) string {
	t.Helper()
	dir := filepath.Join(projectRoot, devopsDir, "playwright")
	if component != "" {
		dir = filepath.Join(dir, component)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "playwright.config.ts")
	if err := os.WriteFile(path, []byte(configBody), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return dir
}

func TestE2E(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"e2e", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "e2e/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_playwright_folder_is_a_clean_no_op", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("expected a clean no-op (exit 0) with no playwright/ folder, got %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "e2e/dry_run_no_playwright_folder", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_missing_tenant", func(t *testing.T) {
		// A discovered suite still requires a resolved tenant/environment
		// before anything else is checked -- the same refusal usage/observe
		// produce with none configured.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "", e2eValidSuiteConfig)
		result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
		}
		golden.Equal(t, "e2e/dry_run_missing_tenant", normalize.Apply(result.Combined))
	})

	t.Run("ambiguous_suite_names_every_component", func(t *testing.T) {
		// Two per-component suites and no --component: the discovery error
		// must name every discovered component so the operator knows what to
		// pass, mirroring resolveProjectComponent's ambiguity error. This
		// resolves before tenant/environment, so no fixture.SeedTenantEnv is
		// needed.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "erun-console", e2eValidSuiteConfig)
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "erun-backend-api", e2eValidSuiteConfig)
		result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an ambiguous suite selection, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "more than one playwright suite found") ||
			!strings.Contains(result.Combined, "erun-console") || !strings.Contains(result.Combined, "erun-backend-api") {
			t.Fatalf("expected the ambiguity error to name every discovered component, got:\n%s", result.Combined)
		}
		golden.Equal(t, "e2e/ambiguous_suite_names_every_component", normalize.Apply(result.Combined))
	})

	t.Run("unknown_component_is_named_in_the_error", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "erun-console", e2eValidSuiteConfig)
		result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "does-not-exist"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an unknown component, got 0: %s", result.Combined)
		}
		golden.Equal(t, "e2e/unknown_component_is_named_in_the_error", normalize.Apply(result.Combined))
	})

	t.Run("real_run_refuses_a_suite_that_disables_https_verification", func(t *testing.T) {
		// The lint check runs before any environment resolution touches the
		// network, so this refuses even though the fixture environment
		// carries no real cluster.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		config := "export default {\n  use: {\n    baseURL: process.env.ERUN_E2E_BASE_URL,\n    ignoreHTTPSErrors: true,\n  },\n};\n"
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "", config)
		result := erun.Run(t, []string{"e2e"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a suite that disables TLS verification, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "ignoreHTTPSErrors") {
			t.Fatalf("expected the refusal to name ignoreHTTPSErrors, got:\n%s", result.Combined)
		}
		golden.Equal(t, "e2e/real_run_refuses_ignore_https_errors", normalize.Apply(result.Combined))
	})

	t.Run("real_run_refuses_a_suite_that_hardcodes_base_url", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		config := "export default {\n  use: {\n    baseURL: 'https://example.com',\n  },\n};\n"
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "", config)
		result := erun.Run(t, []string{"e2e"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a suite that hardcodes baseURL, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "hardcodes baseURL") {
			t.Fatalf("expected the refusal to name the hardcoded baseURL, got:\n%s", result.Combined)
		}
		golden.Equal(t, "e2e/real_run_refuses_hardcoded_base_url", normalize.Apply(result.Combined))
	})

	t.Run("real_run_refuses_when_the_environment_is_not_deployed", func(t *testing.T) {
		// Preconditions are checked before Playwright ever starts, even under
		// --dry-run: they are read-only, and dry-run's whole value here is
		// showing a real refusal instead of assuming success.
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedPlaywrightSuite(t, setup.Cwd, "team-devops", "", e2eValidSuiteConfig)
		stubs := setup.Cwd + "/stubs"
		envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubs, fixture.KubectlDeployedStubSpec{
			DeploymentName:     "team-devops",
			DeploymentNotFound: true,
		})...)
		result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for an undeployed environment, got 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "is not deployed") {
			t.Fatalf("expected the refusal to name the environment as not deployed, got:\n%s", result.Combined)
		}
		golden.Equal(t, "e2e/real_run_refuses_when_not_deployed", normalize.Apply(result.Combined))
	})
}
