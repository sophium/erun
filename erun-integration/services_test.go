package integration

import (
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// servicesStubResponses is the canned pair of `kubectl get ... -o json` bodies
// the services real-run scenarios share: three Services, one of which an
// erun-expose Ingress fronts, plus an Ingress that is not an expose Ingress at
// all (no expose- prefix) and so must not be attributed to any Service.
//
// The exposed Service is deliberately not named after the Ingress's label:
// "pw-api" is fronted by the Ingress "expose-api", which is the repo-native
// chart shape the derivation `<tenant>-<service>` gets wrong. A listing that
// re-derived instead of reading the Ingress backend would report "team-api"
// here, a Service that does not exist.
func servicesStubResponses() map[string]string {
	return map[string]string{
		"service": `{"items":[{"metadata":{"name":"pw-api"},"spec":{"type":"ClusterIP","ports":[{"name":"http","port":80,"protocol":"TCP"}]}},{"metadata":{"name":"team-mcp"},"spec":{"type":"ClusterIP","ports":[{"name":"mcp","port":80,"protocol":"TCP"}]}},{"metadata":{"name":"pw-web"},"spec":{"type":"ClusterIP","ports":[{"name":"http","port":80,"protocol":"TCP"}]}}]}`,
		"ingress": `{"items":[{"metadata":{"name":"expose-api"},"spec":{"rules":[{"host":"api.team-dev.services.erunpaas.com","http":{"paths":[{"backend":{"service":{"name":"pw-api","port":{"number":80}}}}]}}],"tls":[{"hosts":["api.team-dev.services.erunpaas.com"],"secretName":"team-dev-wildcard-tls"}]}},{"metadata":{"name":"runtime-dashboard"},"spec":{"rules":[{"host":"dash.team-dev.internal","http":{"paths":[{"backend":{"service":{"name":"pw-web","port":{"number":80}}}}]}}]}}]}`,
	}
}

func TestServices(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"services", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		// The listing's own plan, before anything is read: the two kubectl gets
		// in the order a real run issues them, and the decision the listing makes
		// about how it matches an exposure to a Service.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"services", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_missing_tenant", func(t *testing.T) {
		// No tenant configured at all: resolution fails before any kubectl call
		// is planned.
		setup := env.New(t)
		result := erun.Run(t, []string{"services", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
		}
		golden.Equal(t, "services/dry_run_missing_tenant", normalize.Apply(result.Combined))
	})

	// real_run_reports_the_exposure_state is the reported gap this scenario
	// exists for: the listing must say which Services are already published,
	// and it must take that from the Ingress's own backend rather than
	// re-deriving <tenant>-<service>. Both claims are visible in one run --
	// pw-api is reported exposed under the label "api", while pw-web, whose
	// only Ingress is not an expose Ingress, is reported plainly.
	t.Run("real_run_reports_the_exposure_state", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, servicesStubResponses())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/real_run_reports_the_exposure_state", normalize.Apply(result.Combined))
	})

	t.Run("real_run_json_carries_the_same_read", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, servicesStubResponses())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/real_run_json_carries_the_same_read", normalize.Apply(result.Combined))
	})

	t.Run("real_run_empty_namespace", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, map[string]string{
			"service": `{"items":[]}`,
			"ingress": `{"items":[]}`,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "services/real_run_empty_namespace", normalize.Apply(result.Combined))
	})

	// real_run_forbidden_is_not_an_empty_environment: the desktop renders this
	// case as a distinct permission-restricted state, and the CLI must not
	// report the same thing a genuinely empty namespace reports. The headline
	// says what could not be read before the kubectl cause follows.
	t.Run("real_run_forbidden_is_not_an_empty_environment", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
			Stderr:   `Error from server (Forbidden): services is forbidden: User "system:serviceaccount:team-dev:default" cannot list resource "services" in API group "" in the namespace "team-dev"`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"services"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a forbidden listing, got 0: %s", result.Combined)
		}
		golden.Equal(t, "services/real_run_forbidden_is_not_an_empty_environment", normalize.Apply(result.Combined))
	})
}
