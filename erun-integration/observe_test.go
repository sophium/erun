package integration

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// observeStubResponses is canned kubectl JSON shared by observe scenarios; the
// Certificate chain ends in a Challenge failing on an RBAC denial.
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

// observeHelmStatusStub agrees with observeStubResponses and SeedTenantEnv, so
// there is no drift unless a scenario mutates one side. Like real helm status
// output, it has no "chart" key.
func observeHelmStatusStub() string {
	return `{"name":"team-devops","namespace":"team-dev","version":3,"info":{"status":"deployed"},` +
		`"config":{"imageOverrides":{"erun-devops":"registry.example/test/erun-devops:1.0.0"},` +
		`"runtime":{"resources":{"limits":{"cpu":"4","memory":"8916Mi"}}}}}`
}

// observeHelmListStub is the helm read that carries chart and app_version.
func observeHelmListStub(chart, appVersion string) string {
	return `[{"name":"team-devops","namespace":"team-dev","chart":"` + chart + `","app_version":"` + appVersion + `"}]`
}

func observeHelmListStubDefault() string {
	return observeHelmListStub("erun-devops-1.0.0", "1.0.0")
}

func TestObserve(t *testing.T) {
	t.Parallel()
	t.Run("help", testObserveHelp)
	t.Run("dry_run", testObserveDryRun)
	t.Run("dry_run_with_secret", testObserveDryRunWithSecret)
	t.Run("dry_run_missing_tenant", testObserveDryRunMissingTenant)
	t.Run("secret_flag_must_be_name_equals_key", testObserveSecretFlagMustBeNameEqualsKey)

	t.Run("real_run_walks_certificate_failure_chain", testObserveRealRunWalksCertificateFailureChain)

	// The stub reproduces kubectl's real retry-storm output verbatim.
	t.Run("real_run_kubectl_unreachable_reports_one_sanitized_line", testObserveRealRunKubectlUnreachableReportsOneSanitizedLine)
	t.Run("real_run_secret_presence_check", testObserveRealRunSecretPresenceCheck)
	t.Run("real_run_secret_missing_key_never_reveals_value", testObserveRealRunSecretMissingKeyNeverRevealsValue)

	t.Run("real_run_text_output_reports_images_release_and_drift", testObserveRealRunTextOutputReportsImagesReleaseAndDrift)

	// The env config records an explicit runtimepod so drift is against a real
	// configured value, not a default.
	t.Run("real_run_reports_release_drift", testObserveRealRunReportsReleaseDrift)

	// An unset runtimepod asserts nothing, so no drift against a default. The empty
	// verdict must survive omitempty in JSON.
	t.Run("real_run_runtime_pod_silent_config_reports_no_drift", testObserveRealRunRuntimePodSilentConfigReportsNoDrift)

	// Only memory diverges, isolating the memory-only finding.
	t.Run("real_run_runtime_pod_configured_mismatch_reports_drift", testObserveRealRunRuntimePodConfiguredMismatchReportsDrift)

	// A confirmed "not found" must differ from a failed read.
	t.Run("real_run_helm_release_not_found", testObserveRealRunHelmReleaseNotFound)

	// An RBAC-denied read names cause and remedy, in drift too, not "not found".
	t.Run("real_run_helm_release_read_forbidden", testObserveRealRunHelmReleaseReadForbidden)

	// A release read but flagged still prints in full, plus a warning.
	t.Run("real_run_text_output_reports_helm_release_warning", testObserveRealRunTextOutputReportsHelmReleaseWarning)

	t.Run("real_run_helm_release_chart_not_erun", testObserveRealRunHelmReleaseChartNotErun)
}

func testObserveHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"observe", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "observe/help", normalize.Apply(result.Combined))
}

func testObserveDryRun(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"observe", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "observe/dry_run", normalize.Apply(result.Combined))
}

func testObserveDryRunWithSecret(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"observe", "--secret", "db-credentials=password", "--dry-run", "-vv"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "observe/dry_run_with_secret", normalize.Apply(result.Combined))
}

func testObserveDryRunMissingTenant(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"observe", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit with no tenant configured, got 0: %s", result.Combined)
	}
	golden.Equal(t, "observe/dry_run_missing_tenant", normalize.Apply(result.Combined))
}

func testObserveSecretFlagMustBeNameEqualsKey(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	result := erun.Run(t, []string{"observe", "--secret", "just-a-name", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a malformed --secret, got 0: %s", result.Combined)
	}
	golden.Equal(t, "observe/secret_flag_must_be_name_equals_key", normalize.Apply(result.Combined))
}

func testObserveRealRunWalksCertificateFailureChain(t *testing.T) {
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
}

func testObserveRealRunKubectlUnreachableReportsOneSanitizedLine(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := setup.Cwd + "/stubs"
	klogFrame := `E0907 17:02:43.157343   81639 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"https://10.0.0.1:6443/api?timeout=32s\": dial tcp 10.0.0.1:6443: i/o timeout"`
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr: klogFrame + "\n" + klogFrame + "\n" + klogFrame + "\n" + klogFrame + "\n" + klogFrame + "\n" +
			`Unable to connect to the server: dial tcp 10.0.0.1:6443: i/o timeout`,
		ExitCode: 1,
	})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"observe"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an unreachable cluster, got 0: %s", result.Combined)
	}
	golden.Equal(t, "observe/real_run_kubectl_unreachable_reports_one_sanitized_line", normalize.Apply(result.Combined))
}

func testObserveRealRunSecretPresenceCheck(t *testing.T) {
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
}

func testObserveRealRunSecretMissingKeyNeverRevealsValue(t *testing.T) {
	// No value from the stub's canned Data may appear in the output.
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
}

func testObserveRealRunTextOutputReportsImagesReleaseAndDrift(t *testing.T) {
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
}

func testObserveRealRunReportsReleaseDrift(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	appendEnvConfig(t, setup, "team", "dev", "runtimepod:\n  cpu: \"4\"\n  memory: 8916Mi\n")
	stubs := setup.Cwd + "/stubs"
	responses := observeStubResponses()
	// The container diverges from what the release records below.
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
}

func testObserveRealRunRuntimePodSilentConfigReportsNoDrift(t *testing.T) {
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
	var payload map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Stdout), &payload); err != nil {
		t.Fatalf("observe --output json did not emit one JSON object: %v\n%s", err, result.Stdout)
	}
	raw, ok := payload["drift"]
	if !ok {
		t.Fatalf("structured result carries no drift verdict, so a consumer cannot read the one the text stream prints: %s", result.Stdout)
	}
	var drift []string
	if err := json.Unmarshal(raw, &drift); err != nil {
		t.Fatalf("drift = %s, want the list of findings: %v", raw, err)
	}
	if len(drift) != 0 {
		t.Fatalf("drift = %v, want no findings for a release that agrees with the env config and the running pod", drift)
	}
	golden.Equal(t, "observe/real_run_runtime_pod_silent_config_reports_no_drift", normalize.Apply(result.Combined))
}

func testObserveRealRunRuntimePodConfiguredMismatchReportsDrift(t *testing.T) {
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
}

func testObserveRealRunHelmReleaseNotFound(t *testing.T) {
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
}

func testObserveRealRunHelmReleaseReadForbidden(t *testing.T) {
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
}

func testObserveRealRunTextOutputReportsHelmReleaseWarning(t *testing.T) {
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
}

func testObserveRealRunHelmReleaseChartNotErun(t *testing.T) {
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
}
