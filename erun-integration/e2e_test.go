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

const e2eSuiteDockerfile = "FROM mcr.microsoft.com/playwright:v1.49.1-noble AS deps\n" +
	"WORKDIR /suite\n" +
	"FROM deps AS e2e\n" +
	"ARG ERUN_E2E_BASE_URL\n" +
	"ARG ERUN_E2E_VERSION\n" +
	"RUN npx playwright test\n"

// e2eValidSuiteConfig passes erun e2e's lint: baseURL is injected and TLS is verified.
const e2eValidSuiteConfig = "export default {\n  use: {\n    baseURL: process.env.ERUN_E2E_BASE_URL,\n  },\n};\n"

// seedE2ESuite writes a suite under <devopsDir>/e2e/<component>.
func seedE2ESuite(t testing.TB, projectRoot, devopsDir, component, dockerfile, configBody string) string {
	t.Helper()
	dir := filepath.Join(projectRoot, devopsDir, "e2e", component)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
		t.Fatalf("write Dockerfile: %v", err)
	}
	if configBody != "" {
		if err := os.WriteFile(filepath.Join(dir, "playwright.config.ts"), []byte(configBody), 0o644); err != nil {
			t.Fatalf("write playwright.config.ts: %v", err)
		}
	}
	return dir
}

// e2eKubectlResponses stubs the precondition reads for a deployed team/dev. The
// .invalid TLD keeps the loopback check off real DNS.
func e2eKubectlResponses(consoleHost, apiHost string) map[string]string {
	return map[string]string{
		"deployment team-devops": "deployment.apps/team-devops",
		"ingress": `{"items":[` +
			`{"metadata":{"name":"expose-erun-console"},"spec":{"rules":[{"host":"` + consoleHost + `","http":{"paths":[{"backend":{"service":{"name":"team-erun-console","port":{"number":80}}}}]}}],"tls":[{"hosts":["` + consoleHost + `"],"secretName":"team-dev-wildcard-tls"}]}},` +
			`{"metadata":{"name":"expose-erun-backend-api"},"spec":{"rules":[{"host":"` + apiHost + `","http":{"paths":[{"backend":{"service":{"name":"team-erun-backend-api","port":{"number":80}}}}]}}],"tls":[{"hosts":["` + apiHost + `"],"secretName":"team-dev-wildcard-tls"}]}}]}`,
		"certificates.cert-manager.io": `{"items":[{"metadata":{"name":"team-dev-wildcard"},"spec":{"secretName":"team-dev-wildcard-tls","dnsNames":["*.team-dev.invalid"]},"status":{"conditions":[{"type":"Ready","status":"True"}]}}]}`,
	}
}

func stubE2EEnvironment(t testing.TB, setup env.Setup, responses map[string]string) (string, []string) {
	t.Helper()
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubKubectlGetJSON(t, stubs, responses)
	fixture.StubBinary(t, stubs, "helm", `{"name":"team-devops","appVersion":"1.4.2"}`)
	return stubs, append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
}

func TestE2E(t *testing.T) {
	t.Parallel()
	t.Run("help", testE2EHelp)
	t.Run("dry_run_no_e2e_folder_is_a_clean_no_op", testE2EDryRunNoE2eFolderIsACleanNoOp)
	t.Run("dry_run_missing_tenant", testE2EDryRunMissingTenant)
	t.Run("unknown_component_is_named_in_the_error", testE2EUnknownComponentIsNamedInTheError)
	t.Run("host_run_playwright_folder_is_refused_with_the_migration", testE2EHostRunPlaywrightFolderIsRefusedWithTheMigration)
	t.Run("dockerfile_without_an_e2e_stage_is_refused", testE2EDockerfileWithoutAnE2eStageIsRefused)
	t.Run("real_run_refuses_a_suite_that_disables_https_verification", testE2ERealRunRefusesASuiteThatDisablesHttpsVerification)
	t.Run("real_run_refuses_a_suite_that_hardcodes_base_url", testE2ERealRunRefusesASuiteThatHardcodesBaseUrl)
	t.Run("dry_run_refuses_when_the_environment_is_not_deployed", testE2EDryRunRefusesWhenTheEnvironmentIsNotDeployed)
	t.Run("dry_run_runs_every_suite_against_the_exposed_services", testE2EDryRunRunsEverySuiteAgainstTheExposedServices)
	t.Run("dry_run_base_url_skips_the_exposure_checks", testE2EDryRunBaseUrlSkipsTheExposureChecks)
	t.Run("dry_run_base_url_without_version_or_named_environment_is_refused", testE2EDryRunBaseUrlWithoutVersionOrNamedEnvironmentIsRefused)
	t.Run("dry_run_base_url_and_version_read_nothing_from_the_environment", testE2EDryRunBaseUrlAndVersionReadNothingFromTheEnvironment)
	t.Run("dry_run_base_url_and_version_need_no_configured_tenant", testE2EDryRunBaseUrlAndVersionNeedNoConfiguredTenant)
	t.Run("real_run_failure_against_base_url_and_version_names_the_url", testE2ERealRunFailureAgainstBaseUrlAndVersionNamesTheUrl)
	t.Run("dry_run_version_replaces_the_deployed_version", testE2EDryRunVersionReplacesTheDeployedVersion)
	t.Run("dry_run_refuses_an_ingress_resolving_to_loopback", testE2EDryRunRefusesAnIngressResolvingToLoopback)
	t.Run("real_run_reports_a_failing_suite_bug_ready_and_runs_the_rest", testE2ERealRunReportsAFailingSuiteBugReadyAndRunsTheRest)
}

func testE2EHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"e2e", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "e2e/help", normalize.Apply(result.Combined))
}

func testE2EDryRunNoE2eFolderIsACleanNoOp(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected a clean no-op (exit 0) with no e2e/ folder, got %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_no_e2e_folder", normalize.Apply(result.Combined))
}

func testE2EDryRunMissingTenant(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, "")
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_missing_tenant", normalize.Apply(result.Combined))
}

func testE2EUnknownComponentIsNamedInTheError(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, "")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-backend-api", e2eSuiteDockerfile, "")
	result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "does-not-exist"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unknown component, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/unknown_component_is_named_in_the_error", normalize.Apply(result.Combined))
}

func testE2EHostRunPlaywrightFolderIsRefusedWithTheMigration(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	legacy := filepath.Join(setup.Cwd, "team-devops", "playwright")
	if err := os.MkdirAll(legacy, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "playwright.config.ts"), []byte(e2eValidSuiteConfig), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected the retired playwright/ layout to be refused, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/host_run_playwright_folder_is_refused_with_the_migration", normalize.Apply(result.Combined))
}

func testE2EDockerfileWithoutAnE2eStageIsRefused(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", "FROM node:22 AS e2e-deps\nRUN true\n", "")
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a Dockerfile with no e2e stage to be refused, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/dockerfile_without_an_e2e_stage_is_refused", normalize.Apply(result.Combined))
}

func testE2ERealRunRefusesASuiteThatDisablesHttpsVerification(t *testing.T) {
	// Lint runs before environment resolution.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	config := "export default {\n  use: {\n    baseURL: process.env.ERUN_E2E_BASE_URL,\n    ignoreHTTPSErrors: true,\n  },\n};\n"
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, config)
	result := erun.Run(t, []string{"e2e"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a suite that disables TLS verification, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/real_run_refuses_ignore_https_errors", normalize.Apply(result.Combined))
}

func testE2ERealRunRefusesASuiteThatHardcodesBaseUrl(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	config := "export default {\n  use: {\n    baseURL: 'https://example.com',\n  },\n};\n"
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, config)
	result := erun.Run(t, []string{"e2e"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a suite that hardcodes baseURL, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/real_run_refuses_hardcoded_base_url", normalize.Apply(result.Combined))
}

func testE2EDryRunRefusesWhenTheEnvironmentIsNotDeployed(t *testing.T) {
	// Preconditions are read-only, so they run even under --dry-run.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	stubs := setup.Cwd + "/stubs"
	envVars := append(setup.Env(), fixture.StubKubectlDeployed(t, stubs, fixture.KubectlDeployedStubSpec{
		DeploymentName:     "team-devops",
		DeploymentNotFound: true,
	})...)
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an undeployed environment, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/real_run_refuses_when_not_deployed", normalize.Apply(result.Combined))
}

func testE2EDryRunRunsEverySuiteAgainstTheExposedServices(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedProjectK8sConfig(t, setup, "e2e:\n  secrets:\n    - id: console-login\n      env: E2E_CONSOLE_PASSWORD\n")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-backend-api", e2eSuiteDockerfile, "")
	_, envVars := stubE2EEnvironment(t, setup, e2eKubectlResponses("console.team-dev.invalid", "api.team-dev.invalid"))
	envVars = append(envVars, "E2E_CONSOLE_PASSWORD=not-a-real-secret")
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "not-a-real-secret") {
		t.Fatalf("the secret's value reached the output:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_runs_every_suite_against_the_exposed_services", normalize.Apply(result.Combined))
}

func testE2EDryRunBaseUrlSkipsTheExposureChecks(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	_, envVars := stubE2EEnvironment(t, setup, map[string]string{"deployment team-devops": "deployment.apps/team-devops"})
	result := erun.Run(t, []string{"e2e", "--dry-run", "--tenant", "team", "--environment", "dev", "--component", "erun-console", "--base-url", "https://console.staging.invalid"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "ERUN_E2E_VERSION=1.4.2") {
		t.Fatalf("expected the named environment's deployed version 1.4.2, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_base_url_skips_the_exposure_checks", normalize.Apply(result.Combined))
}

func testE2EDryRunBaseUrlWithoutVersionOrNamedEnvironmentIsRefused(t *testing.T) {
	// A --base-url may serve another deployment, so the current scope's version
	// is not assumed.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "erun-console", "--base-url", "https://console.production.invalid"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected --base-url without --version or a named environment to be refused, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "--version") {
		t.Fatalf("expected the refusal to ask for --version, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_base_url_without_version_or_named_environment_is_refused", normalize.Apply(result.Combined))
}

func testE2EDryRunBaseUrlAndVersionReadNothingFromTheEnvironment(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	_, envVars := stubE2EEnvironment(t, setup, map[string]string{})
	result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "erun-console", "--base-url", "https://console.production.invalid", "--version", "1.0.305"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "ERUN_E2E_VERSION=1.0.305") || strings.Contains(result.Combined, "1.4.2") {
		t.Fatalf("expected the suites to expect --version 1.0.305 and never the current scope's 1.4.2, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_base_url_and_version_read_nothing_from_the_environment", normalize.Apply(result.Combined))
}

func testE2EDryRunBaseUrlAndVersionNeedNoConfiguredTenant(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "erun-console", "--base-url", "https://console.production.invalid", "--version", "1.0.305"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("expected no configured tenant to be needed, got exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "ERUN_E2E_VERSION=1.0.305") || strings.Contains(result.Combined, "tenant is not configured") {
		t.Fatalf("expected the suite to run with --version 1.0.305 and no tenant lookup, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_base_url_and_version_need_no_configured_tenant", normalize.Apply(result.Combined))
}

func testE2ERealRunFailureAgainstBaseUrlAndVersionNamesTheUrl(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	stubs := filepath.Join(setup.Cwd, "stubs")
	fixture.StubBinaryWithScript(t, stubs, "docker", `printf '%s\n' '#9 12.41   1 failed'; exit 1`)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"--output", "json", "e2e", "--component", "erun-console", "--base-url", "https://console.production.invalid", "--version", "1.0.305"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a failing suite to fail the run, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "failed against https://console.production.invalid at version 1.0.305") ||
		!strings.Contains(result.Combined, `"baseUrl": "https://console.production.invalid"`) || strings.Contains(result.Combined, `"tenant"`) {
		t.Fatalf("expected the summary and JSON result to name the base URL and no tenant, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/real_run_failure_against_base_url_and_version_names_the_url", normalize.Apply(result.Combined))
}

func testE2EDryRunVersionReplacesTheDeployedVersion(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	_, envVars := stubE2EEnvironment(t, setup, e2eKubectlResponses("console.team-dev.invalid", "api.team-dev.invalid"))
	result := erun.Run(t, []string{"e2e", "--dry-run", "--component", "erun-console", "--version", "1.0.305"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "ERUN_E2E_VERSION=1.0.305") || strings.Contains(result.Combined, "1.4.2") {
		t.Fatalf("expected the suites to expect --version 1.0.305 and never the deployed 1.4.2, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_version_replaces_the_deployed_version", normalize.Apply(result.Combined))
}

func testE2EDryRunRefusesAnIngressResolvingToLoopback(t *testing.T) {
	// A docker build without host networking cannot reach 127.0.0.1.
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, "")
	_, envVars := stubE2EEnvironment(t, setup, e2eKubectlResponses("localhost", "api.team-dev.invalid"))
	result := erun.Run(t, []string{"e2e", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a loopback ingress to be refused, got 0: %s", result.Combined)
	}
	if !strings.Contains(result.Combined, "a loopback address") {
		t.Fatalf("expected the refusal to name the loopback address, got:\n%s", result.Combined)
	}
	golden.Equal(t, "e2e/dry_run_refuses_an_ingress_resolving_to_loopback", normalize.Apply(result.Combined))
}

func testE2ERealRunReportsAFailingSuiteBugReadyAndRunsTheRest(t *testing.T) {
	setup := env.New(t)
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-console", e2eSuiteDockerfile, e2eValidSuiteConfig)
	seedE2ESuite(t, setup.Cwd, "team-devops", "erun-backend-api", e2eSuiteDockerfile, "")
	stubs, envVars := stubE2EEnvironment(t, setup, e2eKubectlResponses("console.team-dev.invalid", "api.team-dev.invalid"))
	fixture.StubBinaryWithScript(t, stubs, "docker", strings.Join([]string{
		`case "$*" in`,
		`  *erun-console/Dockerfile*)`,
		`    printf '%s\n' '#9 12.40   1) [chromium] > signin.spec.ts:8:3 > signs in through the issuer'`,
		`    printf '%s\n' '#9 12.41   1 failed'`,
		`    printf '%s\n' 'ERROR: failed to solve: process "/bin/sh -c npx playwright test" did not complete successfully: exit code: 1' >&2`,
		`    exit 1 ;;`,
		`  *) printf '%s\n' '#9 3.10   4 passed' ;;`,
		`esac`,
	}, "\n"))
	envVars = append(envVars, fixture.StubEnv(stubs, "docker")...)
	result := erun.Run(t, []string{"e2e"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a failing suite to fail the run, got 0: %s", result.Combined)
	}
	golden.Equal(t, "e2e/real_run_reports_a_failing_suite_bug_ready_and_runs_the_rest", normalize.Apply(result.Combined))
}
