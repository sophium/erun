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
// shared by the observe real-run scenarios: one pod running the runtime
// container (named and imaged the way the real erun-devops chart names it,
// so the release-vs-pod image drift check has a real name to match on), one
// quota, one limit range, one ingress (routing to the Service below, so the
// two reads agree the way they do in a real namespace), two Services, and a
// Certificate that is not Ready.
// The CertificateRequest -> Order -> Challenge chain resolves to a Challenge
// whose reason is an RBAC denial on the webhook solver — the exact failure
// class #1138 exists to surface without three more hand-built kubectl calls.
func observeStubResponses() map[string]string {
	return map[string]string{
		"pods":                                `{"items":[{"metadata":{"name":"team-devops-abc123"},"spec":{"containers":[{"name":"erun-devops","resources":{"limits":{"cpu":"4","memory":"8916Mi"}}}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"erun-devops","image":"registry.example/test/erun-devops:1.0.0","restartCount":0,"ready":true,"state":{"running":{"startedAt":"2024-01-01T00:00:00Z"}}}]}}]}`,
		"resourcequota":                       `{"items":[{"metadata":{"name":"erun-quota"},"status":{"hard":{"limits.cpu":"4"},"used":{"limits.cpu":"1"}}}]}`,
		"limitrange":                          `{"items":[{"metadata":{"name":"erun-limits"},"spec":{"limits":[{"type":"Container","default":{"cpu":"1"},"defaultRequest":{"cpu":"100m"}}]}}]}`,
		"ingress":                             `{"items":[{"metadata":{"name":"web"},"spec":{"rules":[{"host":"dev.example.test","http":{"paths":[{"backend":{"service":{"name":"team-web","port":{"number":80}}}}]}}],"tls":[{"hosts":["dev.example.test"],"secretName":"web-tls"}]}}]}`,
		"service":                             `{"items":[{"metadata":{"name":"team-web"},"spec":{"type":"ClusterIP","ports":[{"name":"http","port":80,"protocol":"TCP"}]}},{"metadata":{"name":"team-mcp"},"spec":{"type":"ClusterIP","ports":[{"port":8080}]}}]}`,
		"certificates.cert-manager.io":        `{"items":[{"metadata":{"name":"wildcard"},"spec":{"secretName":"wildcard-tls","dnsNames":["*.dev.example.test"]},"status":{"conditions":[{"type":"Ready","status":"False","reason":"Issuing","message":"waiting for order to complete"}]}}]}`,
		"certificaterequests.cert-manager.io": `{"items":[{"metadata":{"name":"wildcard-abc","creationTimestamp":"2024-01-01T00:00:00Z","labels":{"cert-manager.io/certificate-name":"wildcard"}}}]}`,
		"orders.acme.cert-manager.io":         `{"items":[{"metadata":{"name":"wildcard-order-1","ownerReferences":[{"kind":"CertificateRequest","name":"wildcard-abc"}]},"status":{"state":"pending"}}]}`,
		"challenges.acme.cert-manager.io":     `{"items":[{"metadata":{"name":"wildcard-challenge-1","ownerReferences":[{"kind":"Order","name":"wildcard-order-1"}]},"spec":{"type":"DNS-01","dnsName":"*.dev.example.test"},"status":{"state":"invalid","reason":"RBAC denied: solvers.acme.cert-manager.io is forbidden: cannot create resource challenges"}}]}`,
	}
}

// observeHelmStatusStub is a `helm status -o json` body for the "team-devops"
// release that agrees in every field with observeStubResponses' pod and with
// SeedTenantEnv's recorded runtimeversion (1.0.0) and default runtimepod
// (DefaultRuntimePodCPU/Memory), so scenarios built on it report no drift
// unless they deliberately mutate one side. It carries no "chart" key at all,
// matching real `helm status -o json` output exactly (see
// fetchObservedHelmRelease's doc comment) rather than the shape a caller
// might assume it has.
func observeHelmStatusStub() string {
	return `{"name":"team-devops","namespace":"team-dev","version":3,"info":{"status":"deployed"},` +
		`"config":{"imageOverrides":{"erun-devops":"registry.example/test/erun-devops:1.0.0"},` +
		`"runtime":{"resources":{"limits":{"cpu":"4","memory":"8916Mi"}}}}}`
}

// observeHelmListStub is a `helm list -o json` body for the "team-devops"
// release: the one real helm read that carries chart and app_version.
func observeHelmListStub(chart, appVersion string) string {
	return `[{"name":"team-devops","namespace":"team-dev","chart":"` + chart + `","app_version":"` + appVersion + `"}]`
}

// observeHelmListStubDefault is observeHelmListStub for the "erun-devops"
// chart at the version observeHelmStatusStub/SeedTenantEnv agree on.
func observeHelmListStubDefault() string {
	return observeHelmListStub("erun-devops-1.0.0", "1.0.0")
}

func TestObserve(t *testing.T) {
	t.Parallel()
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
		fixture.StubHelmObserve(t, stubs, observeHelmStatusStub(), observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
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
		fixture.StubHelmObserve(t, stubs, observeHelmStatusStub(), observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
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
		fixture.StubHelmObserve(t, stubs, observeHelmStatusStub(), observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--secret", "db-credentials=other-key", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if got := result.Combined; strings.Contains(got, "c2VjcmV0") {
			t.Fatalf("secret value leaked into output: %s", got)
		}
		golden.Equal(t, "observe/real_run_secret_missing_key_never_reveals_value", normalize.Apply(result.Combined))
	})

	// real_run_text_output_reports_images_release_and_drift locks the
	// human-readable rendering of the new sections (container images, the
	// helm release, and a clean "no drift" verdict) with observeHelmStatusStub's
	// baseline, which agrees with the pod stub and SeedTenantEnv on every field.
	t.Run("real_run_text_output_reports_images_release_and_drift", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		fixture.StubHelmObserve(t, stubs, observeHelmStatusStub(), observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_text_output_reports_images_release_and_drift", normalize.Apply(result.Combined))
	})

	// real_run_reports_release_drift is the scenario #1448 exists for: a helm
	// release's own recorded values disagreeing with the running container
	// (a hand-patched, out-of-band image) and with the env config's recorded
	// runtimeversion/runtimepod must be named, not left for the reader to spot
	// by comparing dumps by eye. The env config records an explicit runtimepod
	// (rather than leaving it unset) so the runtimepod drift below reflects a
	// real configured value disagreeing with the release, not silence compared
	// against a manufactured default.
	t.Run("real_run_reports_release_drift", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"4\"\n  memory: 8916Mi\n")
		stubs := setup.Cwd + "/stubs"
		responses := observeStubResponses()
		// The running container's image and resource limits diverge from what
		// the release itself records below (1.0.0 / 4 CPU / 8916Mi), the exact
		// "hand-patched" and "resized without a matching redeploy" shapes #1448
		// found by hand with kubectl/helm.
		responses["pods"] = `{"items":[{"metadata":{"name":"team-devops-abc123"},"spec":{"containers":[{"name":"erun-devops","resources":{"limits":{"cpu":"4","memory":"8916Mi"}}}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"erun-devops","image":"10.43.0.100:5000/sophium/erun-devops:1.0.0-tini","restartCount":0,"ready":true,"state":{"running":{"startedAt":"2024-01-01T00:00:00Z"}}}]}}]}`
		fixture.StubKubectlGetJSON(t, stubs, responses)
		statusStub := `{"name":"team-devops","namespace":"team-dev","version":5,"info":{"status":"deployed"},` +
			`"config":{"imageOverrides":{"erun-devops":"registry.example/test/erun-devops:1.0.0"},` +
			`"runtime":{"resources":{"limits":{"cpu":"8","memory":"16384Mi"}}}}}`
		fixture.StubHelmObserve(t, stubs, statusStub, observeHelmListStub("erun-devops-1.0.202", "1.0.202"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_reports_release_drift", normalize.Apply(result.Combined))
	})

	// real_run_runtime_pod_silent_config_reports_no_drift is the false-positive
	// regression this scenario exists for: an env config that never recorded a
	// runtimepod (the SeedTenantEnv default, and the in-pod reality per
	// runtime_resources.go's NormalizeRuntimePodResources doc) asserts nothing
	// about the pod's shape, so a release sized well above the package's
	// DefaultRuntimePodCPU/Memory (4 / 8916Mi) must report no runtimepod drift —
	// comparing the release against a manufactured default nobody configured is
	// exactly the bug, not the fix.
	t.Run("real_run_runtime_pod_silent_config_reports_no_drift", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		responses := observeStubResponses()
		responses["pods"] = `{"items":[{"metadata":{"name":"team-devops-abc123"},"spec":{"containers":[{"name":"erun-devops","resources":{"limits":{"cpu":"12","memory":"23552Mi"}}}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"erun-devops","image":"registry.example/test/erun-devops:1.0.0","restartCount":0,"ready":true,"state":{"running":{"startedAt":"2024-01-01T00:00:00Z"}}}]}}]}`
		fixture.StubKubectlGetJSON(t, stubs, responses)
		statusStub := `{"name":"team-devops","namespace":"team-dev","version":3,"info":{"status":"deployed"},` +
			`"config":{"imageOverrides":{"erun-devops":"registry.example/test/erun-devops:1.0.0"},` +
			`"runtime":{"resources":{"limits":{"cpu":"12","memory":"23552Mi"}}}}}`
		fixture.StubHelmObserve(t, stubs, statusStub, observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_runtime_pod_silent_config_reports_no_drift", normalize.Apply(result.Combined))
	})

	// real_run_runtime_pod_configured_mismatch_reports_drift is the other
	// direction of the same regression: once the env config actually records a
	// runtimepod, a release that disagrees with it must still be flagged. CPU
	// agrees and only memory diverges, so the golden isolates the memory-only
	// finding instead of always pairing it with a CPU one.
	t.Run("real_run_runtime_pod_configured_mismatch_reports_drift", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"4\"\n  memory: 2048Mi\n")
		stubs := setup.Cwd + "/stubs"
		responses := observeStubResponses()
		responses["pods"] = `{"items":[{"metadata":{"name":"team-devops-abc123"},"spec":{"containers":[{"name":"erun-devops","resources":{"limits":{"cpu":"4","memory":"4096Mi"}}}]},"status":{"phase":"Running","conditions":[{"type":"Ready","status":"True"}],"containerStatuses":[{"name":"erun-devops","image":"registry.example/test/erun-devops:1.0.0","restartCount":0,"ready":true,"state":{"running":{"startedAt":"2024-01-01T00:00:00Z"}}}]}}]}`
		fixture.StubKubectlGetJSON(t, stubs, responses)
		statusStub := `{"name":"team-devops","namespace":"team-dev","version":3,"info":{"status":"deployed"},` +
			`"config":{"imageOverrides":{"erun-devops":"registry.example/test/erun-devops:1.0.0"},` +
			`"runtime":{"resources":{"limits":{"cpu":"4","memory":"4096Mi"}}}}}`
		fixture.StubHelmObserve(t, stubs, statusStub, observeHelmListStubDefault())
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_runtime_pod_configured_mismatch_reports_drift", normalize.Apply(result.Combined))
	})

	// real_run_helm_release_not_found confirms an absent release is reported
	// as a confirmed "not found", distinct from a read that failed, and still
	// flags that the env config expected one (#1448's "no dead ends" bar: an
	// empty section must not look identical whether nothing is deployed or
	// observe simply could not look).
	t.Run("real_run_helm_release_not_found", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{Stderr: `Error: release: not found`, ExitCode: 1})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_helm_release_not_found", normalize.Apply(result.Combined))
	})

	// real_run_helm_release_read_forbidden confirms an RBAC-denied read names
	// the cause and the remedy instead of leaving the release section empty in
	// a way indistinguishable from "nothing deployed here" — and that the
	// drift array says the same thing, instead of collapsing to the identical
	// "not found" line real_run_helm_release_not_found's golden carries.
	t.Run("real_run_helm_release_read_forbidden", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{
			Stderr:   `Error: query: failed to query with labels: secrets is forbidden: User "system:serviceaccount:team-dev:erun" cannot list resource "secrets" in API group "" in the namespace "team-dev"`,
			ExitCode: 1,
		})
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_helm_release_read_forbidden", normalize.Apply(result.Combined))
	})

	// real_run_text_output_reports_helm_release_warning locks the human-
	// readable rendering of a release that was read successfully (Found:
	// true) but flagged as not looking like erun's own chart: the full
	// release details must still print, plus a warning line — never the
	// "could not read" wording that path uses when nothing was read at all.
	t.Run("real_run_text_output_reports_helm_release_warning", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		statusStub := `{"name":"team-devops","namespace":"team-dev","version":1,"info":{"status":"deployed"},"config":{}}`
		fixture.StubHelmObserve(t, stubs, statusStub, observeHelmListStub("unrelated-app-2.3.0", "2.3.0"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_text_output_reports_helm_release_warning", normalize.Apply(result.Combined))
	})

	// real_run_helm_release_chart_not_erun confirms a release found under the
	// expected name but installed from an unrelated chart is flagged rather
	// than silently reported as if it were erun's own runtime release.
	t.Run("real_run_helm_release_chart_not_erun", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		stubs := setup.Cwd + "/stubs"
		fixture.StubKubectlGetJSON(t, stubs, observeStubResponses())
		statusStub := `{"name":"team-devops","namespace":"team-dev","version":1,"info":{"status":"deployed"},"config":{}}`
		fixture.StubHelmObserve(t, stubs, statusStub, observeHelmListStub("unrelated-app-2.3.0", "2.3.0"))
		envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm")...)
		result := erun.Run(t, []string{"observe", "--output", "json"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "observe/real_run_helm_release_chart_not_erun", normalize.Apply(result.Combined))
	})
}
