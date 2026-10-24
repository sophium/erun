package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// hostnameAPIStubServer is an erun-backend-api double for the platform-route DNS write.
func hostnameAPIStubServer(t testing.TB) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		calls = append(calls, "GET /v1/environments")
		mu.Unlock()
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"environmentId": "env-1", "tenantId": "tenant-1", "name": "dev", "type": "runtime", "status": "running", "createdAt": "2024-01-01T00:00:00Z", "updatedAt": "2024-01-01T00:00:00Z"},
		})
	})
	mux.HandleFunc("PUT /v1/environments/env-1/hostname", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, "PUT /v1/environments/env-1/hostname targetIp="+body["targetIp"])
		mu.Unlock()
		_ = json.NewEncoder(w).Encode(map[string]any{"hostname": "*.team-dev.services.erunpaas.com", "targetIp": body["targetIp"]})
	})
	mux.HandleFunc("DELETE /v1/environments/env-1/hostname", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		mu.Lock()
		calls = append(calls, "DELETE /v1/environments/env-1/hostname")
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server, &calls
}

func TestExpose(t *testing.T) {
	t.Parallel()
	t.Run("help", testExposeHelp)
	t.Run("dry_run", testExposeDryRun)
	t.Run("dry_run_backend_service_overrides_the_derived_name", testExposeDryRunBackendServiceOverridesTheDerivedName)
	t.Run("dry_run_no_tls", testExposeDryRunNoTLS)
	t.Run("dry_run_with_tls_provisioning", testExposeDryRunWithTLSProvisioning)
	t.Run("dry_run_cross_cluster", testExposeDryRunCrossCluster)
	t.Run("requires_platform_config", testExposeRequiresPlatformConfig)
	t.Run("requires_platform_env", testExposeRequiresPlatformEnv)
	t.Run("real_run_via_stubs", testExposeRealRunViaStubs)
	t.Run("skip_if_unconfigured_no_platform", testExposeSkipIfUnconfiguredNoPlatform)
	t.Run("skip_if_unconfigured_with_platform", testExposeSkipIfUnconfiguredWithPlatform)
	t.Run("dry_run_platform_override_no_project", testExposeDryRunPlatformOverrideNoProject)
	t.Run("platform_override_requires_both", testExposePlatformOverrideRequiresBoth)
	t.Run("skip_if_unconfigured_no_project", testExposeSkipIfUnconfiguredNoProject)
	t.Run("requires_project_without_override_or_skip", testExposeRequiresProjectWithoutOverrideOrSkip)
	t.Run("real_run_via_platform_alias", testExposeRealRunViaPlatformAlias)
	t.Run("dry_run_via_platform_alias", testExposeDryRunViaPlatformAlias)
	t.Run("dry_run_via_platform_alias_explicit", testExposeDryRunViaPlatformAliasExplicit)
	t.Run("dry_run_platform_override_beats_configured_alias", testExposeDryRunPlatformOverrideBeatsConfiguredAlias)
	t.Run("ambiguous_erun_alias_without_disambiguation", testExposeAmbiguousErunAliasWithoutDisambiguation)
	t.Run("requires_ip", testExposeRequiresIP)
}

func testExposeHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"expose", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/help", normalize.Apply(result.Combined))
}

func testExposeDryRun(t *testing.T) {
	// TLS without DNS-01 broker flags resolves to http-only and says why.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "expose/dry_run", normalize.Apply(result.Combined))
}

// The public host stays the logical label; only the Ingress backend moves to --backend-service.
func testExposeDryRunBackendServiceOverridesTheDerivedName(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--backend-service", "pw-api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/dry_run_backend_service_overrides_the_derived_name", normalize.Apply(result.Combined))
}

func testExposeDryRunNoTLS(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--no-tls", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "expose/dry_run_no_tls", normalize.Apply(result.Combined))
}

func testExposeDryRunWithTLSProvisioning(t *testing.T) {
	// The token file's content must never appear in the trace, only its path.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	tokenPath := filepath.Join(setup.Cwd, "dns01-token")
	mustWriteFile(t, tokenPath, "test-dns01-broker-token\n")
	result := erun.Run(t, []string{
		"expose", "team", "dev", "api", "--ip", "203.0.113.10",
		"--dns01-token-file", tokenPath,
		"--dns01-broker-url", "https://api.frs-prod.services.erunpaas.com/v1/dns01",
		"--acme-email", "admin@example.com",
		"--dry-run",
	}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "expose/dry_run_with_tls_provisioning", normalize.Apply(result.Combined))
}

func testExposeDryRunCrossCluster(t *testing.T) {
	// The DNS write runs against the platform env's context, the Ingress against the target env's.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedTenantEnvWithContext(t, setup, "frs", "prod", "platform-context")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	golden.Equal(t, "expose/dry_run_cross_cluster", normalize.Apply(result.Combined))
}

func testExposeRequiresPlatformConfig(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without a platform block, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/requires_platform_config", normalize.Apply(result.Combined))
}

func testExposeRequiresPlatformEnv(t *testing.T) {
	// Without platform.env the DNS write would run `kubectl -n "" exec` and misroute.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without platform.env, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/requires_platform_env", normalize.Apply(result.Combined))
}

func testExposeRealRunViaStubs(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/real_run_via_stubs", normalize.Apply(result.Combined))
}

func testExposeSkipIfUnconfiguredNoPlatform(t *testing.T) {
	// A caller composing expose may not know whether the project is a platform deployment.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/skip_if_unconfigured_no_platform", normalize.Apply(result.Combined))
}

func testExposeSkipIfUnconfiguredWithPlatform(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/skip_if_unconfigured_with_platform", normalize.Apply(result.Combined))
}

func testExposeDryRunPlatformOverrideNoProject(t *testing.T) {
	// A hosted deploy Job has no checkout to read .erun/config.yaml from.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"expose", "team", "dev", "api", "--ip", "203.0.113.10",
		"--services-zone", "services.erunpaas.com", "--platform-namespace", "frs-prod", "--dry-run",
	},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/dry_run_platform_override_no_project", normalize.Apply(result.Combined))
}

func testExposePlatformOverrideRequiresBoth(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{
		"expose", "team", "dev", "api", "--ip", "203.0.113.10",
		"--services-zone", "services.erunpaas.com", "--dry-run",
	},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with only --services-zone set, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/platform_override_requires_both", normalize.Apply(result.Combined))
}

func testExposeSkipIfUnconfiguredNoProject(t *testing.T) {
	// A "cannot find git project" failure must not pre-empt the skip decision.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/skip_if_unconfigured_no_project", normalize.Apply(result.Combined))
}

func testExposeRequiresProjectWithoutOverrideOrSkip(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/requires_project_without_override_or_skip", normalize.Apply(result.Combined))
}

func testExposeRealRunViaPlatformAlias(t *testing.T) {
	// Substring assertions: the stub server's ephemeral port makes its URL vary.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	server, calls := hostnameAPIStubServer(t)
	platformAlias(t, setup, server)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if !strings.Contains(result.Combined, "exposed team/dev service api at http://api.team-dev.services.erunpaas.com") {
		t.Fatalf("expected the exposed hostname in output, got:\n%s", result.Combined)
	}
	want := []string{"GET /v1/environments", "PUT /v1/environments/env-1/hostname targetIp=127.0.0.1"}
	if got := *calls; len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("stub server calls = %v, want %v", got, want)
	}
}

func testExposeDryRunViaPlatformAlias(t *testing.T) {
	// A configured erun alias, with no zone/namespace override, routes the DNS write through the platform API.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/dry_run_via_platform_alias", normalize.Apply(result.Combined))
}

func testExposeDryRunViaPlatformAliasExplicit(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--erun-alias", "erun+test@erun", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/dry_run_via_platform_alias_explicit", normalize.Apply(result.Combined))
}

func testExposeDryRunPlatformOverrideBeatsConfiguredAlias(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
	result := erun.Run(t, []string{
		"expose", "team", "dev", "api", "--ip", "203.0.113.10",
		"--services-zone", "services.erunpaas.com", "--platform-namespace", "frs-prod", "--dry-run",
	},
		erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "expose/dry_run_platform_override_beats_configured_alias", normalize.Apply(result.Combined))
}

func testExposeAmbiguousErunAliasWithoutDisambiguation(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
	seedTwoERunCloudProviderAliases(t, setup)
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit with two erun aliases configured and none named, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/ambiguous_erun_alias_without_disambiguation", normalize.Apply(result.Combined))
}

func testExposeRequiresIP(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedGitRepo(t, setup.Cwd)
	fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n")
	result := erun.Run(t, []string{"expose", "team", "dev", "api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected non-zero exit without --ip, got 0:\n%s", result.Combined)
	}
	golden.Equal(t, "expose/requires_ip", normalize.Apply(result.Combined))
}
