package integration

import (
	"encoding/base64"
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

func testDeployDryRunWithMCPAuthPublicKey(t *testing.T) {
	// The key ships as a <release>-mcp-auth Secret; mcpAuth.* values go to the runtime chart only.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := filepath.Join(t.TempDir(), "desktopid.pub")
	if err := os.WriteFile(keyPath, []byte("-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n"), 0o600); err != nil {
		t.Fatalf("write public key fixture: %v", err)
	}
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--mcp-auth-public-key", keyPath, "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_with_mcp_auth_public_key", normalize.Apply(result.Combined))
}

func testDeployDryRunRethreadsRecordedMCPAuthPublicKey(t *testing.T) {
	// Auth is sticky: deploy does not --reuse-values, so omitting mcpAuth values
	// would open the edge, whose `raw` tool runs commands in the pod.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := seedMCPAuthPublicKey(t, setup)
	appendEnvConfig(t, setup, "team", "dev", "mcpauthpublickeypath: "+keyPath+"\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_rethreads_recorded_mcp_auth_public_key", normalize.Apply(result.Combined))
}

func testDeployDryRunNoMCPAuthDropsRecordedKey(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := seedMCPAuthPublicKey(t, setup)
	appendEnvConfig(t, setup, "team", "dev", "mcpauthpublickeypath: "+keyPath+"\n")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-mcp-auth", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_no_mcp_auth_drops_recorded_key", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusesToDowngradeLiveMCPAuth(t *testing.T) {
	// Only the live release shows auth here. env.Env() sets the probe override to
	// unknown globally, which is why sibling scenarios never reach helm.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refuses_to_downgrade_live_mcp_auth", normalize.Apply(result.Combined))
}

func testDeployDryRunNoMCPAuthClearsLiveMCPAuth(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--no-mcp-auth", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_no_mcp_auth_clears_live_mcp_auth", normalize.Apply(result.Combined))
}

func testDeployRealRunRecordsThenClearsMCPAuthPublicKey(t *testing.T) {
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := seedMCPAuthPublicKey(t, setup)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)

	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.1", "--mcp-auth-public-key", keyPath}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if body := readEnvConfig(t, setup, "team", "dev"); !strings.Contains(body, "mcpauthpublickeypath: "+keyPath) {
		t.Fatalf("expected the env config to record mcpauthpublickeypath: %s, got:\n%s", keyPath, body)
	}

	result = erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.2", "--no-mcp-auth"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if body := readEnvConfig(t, setup, "team", "dev"); strings.Contains(body, "mcpauthpublickeypath:") {
		t.Fatalf("expected --no-mcp-auth to clear mcpauthpublickeypath, got:\n%s", body)
	}
}

func testDeployRealRunFailedRolloutStillRecordsMCPAuthPublicKey(t *testing.T) {
	// The key is recorded where helm applies it, so a failed rollout still lets
	// the next deploy rethread it instead of being refused.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := seedMCPAuthPublicKey(t, setup)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	// Silent: stub stderr would race erun's ordered output and break the golden.
	fixture.StubBinaryAdvanced(t, stubs, "helm", fixture.StubBinarySpec{ExitCode: 1})
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)

	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.1", "--mcp-auth-public-key", keyPath}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a failed rollout:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_failed_rollout_still_records_mcp_auth_public_key", normalize.Apply(result.Combined))
	body := readEnvConfig(t, setup, "team", "dev")
	if !strings.Contains(body, "mcpauthpublickeypath: "+keyPath) {
		t.Fatalf("expected the failed rollout to still record mcpauthpublickeypath: %s, got:\n%s", keyPath, body)
	}
	// The runtime version is recorded only after a successful rollout.
	if strings.Contains(body, "runtimeversion: 1.0.1") {
		t.Fatalf("expected the failed rollout to leave the recorded runtime version alone, got:\n%s", body)
	}

	recovery := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
	result = erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.2", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: recovery})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_recovers_after_a_failed_authenticated_rollout", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusalNamesTheDesktopIdentityKeyTheReleaseTrusts(t *testing.T) {
	// The refusal reads the trusted key from the release's Secret and, when it is
	// this host's desktop identity, names the path. kubectl stands in for the read.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	trustedKey := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAdesktopidentitydesktopidentitydesktopidenti=\n-----END PUBLIC KEY-----\n"
	identityPath := fixture.SeedDesktopIdentityPublicKey(t, setup, trustedKey)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", base64.StdEncoding.EncodeToString([]byte(trustedKey)))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	envVars = append(envVars, "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refusal_names_the_desktop_identity_key_the_release_trusts", normalize.Apply(result.Combined))
	// Normalization collapses the identity path, so assert it on the raw capture.
	if !strings.Contains(result.Combined, "--mcp-auth-public-key "+identityPath) {
		t.Fatalf("expected the refusal to name %s as the key to re-supply, got:\n%s", identityPath, result.Combined)
	}
}

func testDeployDryRunRefusalNamesTheSecretWhenTheKeyIsNotThisHostS(t *testing.T) {
	// A different host key would rotate trust, so the message names Secret and fingerprint.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	fixture.SeedDesktopIdentityPublicKey(t, setup, "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAanotherhostanotherhostanotherhostanotherho=\n-----END PUBLIC KEY-----\n")
	trustedKey := "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAdesktopidentitydesktopidentitydesktopidenti=\n-----END PUBLIC KEY-----\n"
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", base64.StdEncoding.EncodeToString([]byte(trustedKey)))
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl")...)
	envVars = append(envVars, "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refusal_names_the_secret_when_the_key_is_not_this_host_s", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusalForAnOIDCReleasePointsAtTheIssuerNotAKey(t *testing.T) {
	// An OIDC-authenticated edge has no local key. No kubectl stub: this arm never
	// reads the key Secret.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	envVars := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|https://issuer.example/realms/team|")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for a deploy that would strip live MCP auth:\n%s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_refusal_for_an_oidc_release_points_at_the_issuer_not_a_key", normalize.Apply(result.Combined))
}

// seedMCPAuthPublicKey writes under the scenario's temp tree so the path normalizes.
func seedMCPAuthPublicKey(t *testing.T, setup env.Setup) string {
	t.Helper()
	keyPath := filepath.Join(setup.Home, "desktopid.pub")
	mustWriteFile(t, keyPath, "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n")
	return keyPath
}
