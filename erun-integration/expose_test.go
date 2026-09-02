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

// hostnameAPIStubServer runs a minimal erun-backend-api double covering the
// two calls the platform-route DNS write path makes (GET /v1/environments to
// resolve the environment id by name, then PUT/DELETE .../hostname), so a
// real-run scenario exercises erun-common/expose_platform_dns.go's actual
// network calls, not only their --dry-run trace.
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
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"expose", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// Happy path: a platform block plus an env yields a complete expose plan
		// with no side effects. TLS is requested by default (no --no-tls) but no
		// DNS-01 broker flags are set, so the plan resolves to http-only and says
		// why, rather than the Ingress claiming https with nothing to ever
		// populate the certificate Secret.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_no_tls", func(t *testing.T) {
		// --no-tls takes the http branch: the plan traces "http-only" and the
		// rendered Ingress carries no tls block, only ingressClassName.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--no-tls", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run_no_tls", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_tls_provisioning", func(t *testing.T) {
		// --dns01-token-file + --dns01-broker-url + --acme-email together
		// provision a per-env cert-manager Issuer + Certificate through the
		// DNS-01 broker, so the wildcard TLS Secret the Ingress references
		// actually gets populated. The token file's content never
		// appears in the trace -- only its path.
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
	})

	t.Run("dry_run_cross_cluster", func(t *testing.T) {
		// The platform env that owns PowerDNS sits on a different cluster than the
		// target env: the wildcard DNS write must exec against the platform env's
		// kube context while the Ingress applies against the target env's, and the
		// two must never collapse into one cross-cluster misroute.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedTenantEnvWithContext(t, setup, "frs", "prod", "platform-context")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "expose/dry_run_cross_cluster", normalize.Apply(result.Combined))
	})

	t.Run("requires_platform_config", func(t *testing.T) {
		// expose only makes sense for a platform deployment; without a platform
		// block it fails with an actionable error rather than resolving a hostname
		// under an unknown zone.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without a platform block, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_platform_config", normalize.Apply(result.Combined))
	})

	t.Run("requires_platform_env", func(t *testing.T) {
		// Without platform.env the per-env wildcard DNS write would exec as
		// `kubectl -n "" exec` and silently misroute, so expose fails fast.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without platform.env, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_platform_env", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_stubs", func(t *testing.T) {
		// Drive the live expose path (the block RunExposeService reaches only after
		// the dry-run short-circuit: RequireKubernetesContext, the pdnsutil
		// replace-rrset exec, and the Host-routing Ingress apply) via a kubectl stub
		// so the real-run execution branch gets covered, not just the dry-run trace.
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
	})

	t.Run("skip_if_unconfigured_no_platform", func(t *testing.T) {
		// --skip-if-unconfigured turns the missing-platform-block hard failure
		// (see requires_platform_config above) into a traced no-op success, the
		// behavior a caller composing expose after another command needs when it
		// cannot know in advance whether the target project is a platform
		// deployment.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_no_platform", normalize.Apply(result.Combined))
	})

	t.Run("skip_if_unconfigured_with_platform", func(t *testing.T) {
		// --skip-if-unconfigured must not change behavior for an actual platform
		// deployment: with a platform block present, it resolves and traces the
		// full plan exactly like the plain dry_run scenario above.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n  authoritativeip: 203.0.113.10\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_with_platform", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_platform_override_no_project", func(t *testing.T) {
		// --services-zone/--platform-namespace supply what a project checkout
		// would otherwise resolve, so expose runs from a directory with no git
		// repo at all -- the shape a hosted deploy Job runs in, which has no
		// checkout to read .erun/config.yaml from.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10",
			"--services-zone", "services.erunpaas.com", "--platform-namespace", "frs-prod", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/dry_run_platform_override_no_project", normalize.Apply(result.Combined))
	})

	t.Run("platform_override_requires_both", func(t *testing.T) {
		// Half the override configured is the same as neither: expose refuses
		// rather than resolving a plan from an incomplete pair.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10",
			"--services-zone", "services.erunpaas.com", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit with only --services-zone set, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/platform_override_requires_both", normalize.Apply(result.Combined))
	})

	t.Run("skip_if_unconfigured_no_project", func(t *testing.T) {
		// --skip-if-unconfigured must cover "no project at all", not just "a
		// project with no platform block" -- a gap that used to mean the deploy
		// Job's --skip-if-unconfigured could not save it because project
		// resolution itself failed outright with "cannot find git project"
		// before the skip decision ever ran.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--skip-if-unconfigured", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/skip_if_unconfigured_no_project", normalize.Apply(result.Combined))
	})

	t.Run("requires_project_without_override_or_skip", func(t *testing.T) {
		// Interactive `erun expose` from a plain, non-git directory still fails
		// fast -- the override flags and --skip-if-unconfigured are opt-in, not
		// a silent relaxation of the default project requirement.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "127.0.0.1", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit outside a git project, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_project_without_override_or_skip", normalize.Apply(result.Combined))
	})

	t.Run("real_run_via_platform_alias", func(t *testing.T) {
		// Drives the platform-route DNS write for real against a stub
		// erun-backend-api: resolvePlatformEnvironmentID's GET
		// /v1/environments lookup and the PUT .../hostname call itself, not
		// only their --dry-run trace. kubectl is still stubbed for the
		// Ingress apply, which is unaffected by the DNS path choice.
		// Substring assertions, not golden.Equal: the stub server's own
		// ephemeral port makes the trace lines that name its URL
		// intrinsically variable (erun-integration/AGENTS.md's third
		// exception to the whole-output-snapshot default), the same reason
		// review_test.go's own real-run scenarios against a stub server use
		// this shape instead of a golden.
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
	})

	t.Run("dry_run_via_platform_alias", func(t *testing.T) {
		// An erun platform alias configured (and no --services-zone/
		// --platform-namespace override -- the hosted deploy Job's own
		// signal for direct pdnsutil access) switches the DNS write from the
		// direct kubectl exec to the platform's own API,
		// automatically, with no extra flag needed.
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
	})

	t.Run("dry_run_via_platform_alias_explicit", func(t *testing.T) {
		// --erun-alias names which configured alias to use; here it is the
		// sole one configured, so behavior matches the automatic case above.
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
	})

	t.Run("dry_run_platform_override_beats_configured_alias", func(t *testing.T) {
		// --services-zone/--platform-namespace (the hosted deploy Job's own
		// signal that it has direct PowerDNS access) always wins over an
		// erun platform alias, even when one happens to be configured --
		// this caller never actually configures one in practice, but the
		// override must still take priority if it did.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedERunCloudProviderAlias(t, setup, "erun+test@erun", "https://api.example.test", "cli-test-client")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--ip", "203.0.113.10",
			"--services-zone", "services.erunpaas.com", "--platform-namespace", "frs-prod", "--dry-run"},
			erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "expose/dry_run_platform_override_beats_configured_alias", normalize.Apply(result.Combined))
	})

	t.Run("ambiguous_erun_alias_without_disambiguation", func(t *testing.T) {
		// Two erun-type aliases configured and neither named refuses rather
		// than silently guessing which credential should perform the DNS
		// write, matching `erun review`'s own ambiguity refusal.
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
	})

	t.Run("requires_ip", func(t *testing.T) {
		// The per-env wildcard record needs a target IP (the env's ingress IP);
		// omitting --ip fails clearly instead of writing an empty record.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		fixture.SeedGitRepo(t, setup.Cwd)
		fixture.SeedProjectK8sConfig(t, setup, "platform:\n  basedomain: erunpaas.com\n  env: frs-prod\n")
		result := erun.Run(t, []string{"expose", "team", "dev", "api", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected non-zero exit without --ip, got 0:\n%s", result.Combined)
		}
		golden.Equal(t, "expose/requires_ip", normalize.Apply(result.Combined))
	})
}
