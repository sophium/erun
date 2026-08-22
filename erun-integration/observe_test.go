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

// observeStubResponses is the canned `kubectl get <resource> -o json` bodies
// shared by the observe real-run scenarios: one pod, one quota, one limit
// range, one ingress, and a Certificate that is not Ready. The
// CertificateRequest -> Order -> Challenge chain resolves to a Challenge
// whose reason is an RBAC denial on the webhook solver — the exact failure
// class #1138 exists to surface without three more hand-built kubectl calls.
func observeStubResponses() map[string]string {
	return map[string]string{
		"pods":                                `{"items":[{"metadata":{"name":"web-0"},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"app","restartCount":0,"ready":true,"state":{"running":{"startedAt":"2024-01-01T00:00:00Z"}}}]}}]}`,
		"resourcequota":                       `{"items":[{"metadata":{"name":"erun-quota"},"status":{"hard":{"limits.cpu":"4"},"used":{"limits.cpu":"1"}}}]}`,
		"limitrange":                          `{"items":[{"metadata":{"name":"erun-limits"},"spec":{"limits":[{"type":"Container","default":{"cpu":"1"},"defaultRequest":{"cpu":"100m"}}]}}]}`,
		"ingress":                             `{"items":[{"metadata":{"name":"web"},"spec":{"rules":[{"host":"dev.example.test"}],"tls":[{"hosts":["dev.example.test"],"secretName":"web-tls"}]}}]}`,
		"certificates.cert-manager.io":        `{"items":[{"metadata":{"name":"wildcard"},"spec":{"secretName":"wildcard-tls","dnsNames":["*.dev.example.test"]},"status":{"conditions":[{"type":"Ready","status":"False","reason":"Issuing","message":"waiting for order to complete"}]}}]}`,
		"certificaterequests.cert-manager.io": `{"items":[{"metadata":{"name":"wildcard-abc","creationTimestamp":"2024-01-01T00:00:00Z","labels":{"cert-manager.io/certificate-name":"wildcard"}}}]}`,
		"orders.acme.cert-manager.io":         `{"items":[{"metadata":{"name":"wildcard-order-1","ownerReferences":[{"kind":"CertificateRequest","name":"wildcard-abc"}]},"status":{"state":"pending"}}]}`,
		"challenges.acme.cert-manager.io":     `{"items":[{"metadata":{"name":"wildcard-challenge-1","ownerReferences":[{"kind":"Order","name":"wildcard-order-1"}]},"spec":{"type":"DNS-01","dnsName":"*.dev.example.test"},"status":{"state":"invalid","reason":"RBAC denied: solvers.acme.cert-manager.io is forbidden: cannot create resource challenges"}}]}`,
	}
}

func TestObserve(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"observe", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"observe", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/dry_run", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_with_secret", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"observe", "--secret", "db-credentials=password", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/dry_run_with_secret", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_missing_tenant", func(t *testing.T) {
		// No tenant configured at all: resolution fails before any kubectl call
		// is even planned.
		setup := env.New(t)
		result := erun.Run(t, []string{"observe", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
		}
		golden.Equal(t, "observe/dry_run_missing_tenant", normalize.Apply(result.Combined))
	})

	t.Run("secret_flag_must_be_name_equals_key", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		result := erun.Run(t, []string{"observe", "--secret", "just-a-name", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a non-zero exit for a malformed --secret, got 0: %s", result.Combined)
		}
		golden.Equal(t, "observe/secret_flag_must_be_name_equals_key", normalize.Apply(result.Combined))
	})

	// real_run_walks_certificate_failure_chain is the scenario #1138 exists
	// for: a Certificate that is not Ready must surface the reason from the
	// Challenge at the bottom of its CertificateRequest -> Order -> Challenge
	// chain, in this one call, rather than requiring the caller to compose
	// that walk from three more hand-built kubectl queries.
	t.Run("real_run_walks_certificate_failure_chain", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_walks_certificate_failure_chain", normalize.Apply(result.Combined))
	})

	t.Run("real_run_secret_presence_check", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		responses := observeStubResponses()
		responses["secret db-credentials"] = `{"data":{"password":"c2VjcmV0"}}`
		fixture.StubKubectlGetJSON(t, stubs, responses)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"observe", "--secret", "db-credentials=password", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_secret_presence_check", normalize.Apply(result.Combined))
	})

	t.Run("real_run_secret_missing_key_never_reveals_value", func(t *testing.T) {
		// The secret exists but the checked key does not: hasKey is false and
		// no value from the stub's canned Data ever appears in the output.
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		responses := observeStubResponses()
		responses["secret db-credentials"] = `{"data":{"password":"c2VjcmV0"}}`
		fixture.StubKubectlGetJSON(t, stubs, responses)
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
		result := erun.Run(t, []string{"observe", "--secret", "db-credentials=other-key", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if got := result.Combined; strings.Contains(got, "c2VjcmV0") {
			t.Fatalf("secret value leaked into output: %s", got)
		}
		golden.Equal(t, "observe/real_run_secret_missing_key_never_reveals_value", normalize.Apply(result.Combined))
	})
}
