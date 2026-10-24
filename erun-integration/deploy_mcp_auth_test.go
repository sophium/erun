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
	// --mcp-auth-public-key makes the runtime deploy require the per-env MCP
	// edge to authenticate bearer tokens signed by the desktop public key:
	// the key is applied out-of-band as a <release>-mcp-auth Secret and the
	// file:// issuer + per-env audience ride as mcpAuth.* helm values on the
	// runtime (team-devops) chart only.
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
	// Regression: a plain version bump of an env whose MCP edge authenticates
	// used to emit no mcpAuth values at all, and since deploy does not
	// --reuse-values the chart defaults turned a publicly reachable,
	// token-authenticated edge (whose `raw` tool runs commands in the pod) into
	// an open one. Auth is now sticky: the env records the key path the last
	// authenticated deploy trusted, and a deploy with no --mcp-auth-public-key
	// rethreads it, rendering the same mcpAuth.* values and re-applying the
	// key Secret.
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
	// Turning authentication off is explicit: --no-mcp-auth resolves no
	// mcpAuth values even though the env has a recorded key, so the rendered
	// release leaves the edge loopback-only. The sibling
	// dry_run_rethreads_recorded_mcp_auth_public_key golden shows the same env
	// keeping auth without the flag.
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
	// The env's MCP auth predates the recorded key (it was enabled by an older
	// desktop deploy), so nothing in the config says "authenticated" — only
	// the live release does. deploy reads it and refuses rather than roll out a
	// release that silently drops authentication, naming the flag that turns it
	// off on purpose. The ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE seam answers the
	// live-release read so the scenario needs no cluster (env.Env() sets it to
	// unknown globally, which is why sibling scenarios never reach helm).
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
	// The opt-out is the way past the downgrade guard: with the same
	// live-auth-enabled release as dry_run_refuses_to_downgrade_live_mcp_auth,
	// --no-mcp-auth resolves and rolls out an unauthenticated edge.
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
	// Persisting the key path is a real-run side effect outside the captured
	// streams, so it is asserted by reading the env config: an authenticated
	// deploy records the path (which is what makes the next redeploy sticky),
	// and --no-mcp-auth forgets it.
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
	// Regression: the key was recorded only after the whole deploy succeeded,
	// so a rollout that failed once helm had already applied it left the live
	// release trusting a key the env could not name — and the next deploy's
	// downgrade refusal had nothing to rethread, blocking the very redeploy
	// that would have healed the environment. The key is now recorded where it
	// is applied, so the failed rollout below still leaves the env naming it and
	// the follow-up deploy rethreads it instead of being refused.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	fixture.SeedDevopsRepo(t, setup, "team", "dev")
	keyPath := seedMCPAuthPublicKey(t, setup)
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinary(t, stubs, "kubectl", "")
	fixture.StubBinary(t, stubs, "docker", "")
	// A silent failure: the stub's own stderr is inherited by the subprocess
	// and races erun's ordered output, so the golden would not be reproducible
	// if it printed one. The exit code alone drives the failure under test.
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
	// The version stays a post-success write: the rollout that would have made
	// it the running one failed.
	if strings.Contains(body, "runtimeversion: 1.0.1") {
		t.Fatalf("expected the failed rollout to leave the recorded runtime version alone, got:\n%s", body)
	}

	// The recovery the record exists for: with the live release still
	// authenticated, the next deploy rethreads the recorded key rather than
	// being refused as a downgrade.
	recovery := append(setup.Env(), "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE=enabled|file:///etc/erun/mcp-auth/desktopid.pub|team-devops-mcp-auth")
	result = erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.2", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: recovery})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_recovers_after_a_failed_authenticated_rollout", normalize.Apply(result.Combined))
}

func testDeployDryRunRefusalNamesTheDesktopIdentityKeyTheReleaseTrusts(t *testing.T) {
	// A release that predates the recorded key can only be recovered by
	// re-supplying the key it already trusts, so the refusal reads that key out
	// of the release's own Secret and — when it is this host's desktop identity
	// — names the path to pass, instead of leaving the operator to work out
	// which file the edge trusts and whether re-supplying it would rotate that
	// trust. The kubectl stub stands in for the Secret read; the live-release
	// answer comes from the ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE seam.
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
	// The identity path is collapsed to a token by normalization, so the
	// golden cannot tell one path from another: assert the concrete one the
	// operator would paste against the raw capture.
	if !strings.Contains(result.Combined, "--mcp-auth-public-key "+identityPath) {
		t.Fatalf("expected the refusal to name %s as the key to re-supply, got:\n%s", identityPath, result.Combined)
	}
}

func testDeployDryRunRefusalNamesTheSecretWhenTheKeyIsNotThisHostS(t *testing.T) {
	// Same refusal from a host whose desktop identity is a different key: the
	// message names the Secret and the fingerprint instead of a path, because
	// pasting this host's key would rotate the edge's trust rather than restore
	// it.
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
	// An env whose edge authenticates against the tenant's OIDC issuer holds no
	// local key at all, so telling it to supply --mcp-auth-public-key would send
	// the operator after a file that should not exist. The refusal names the
	// issuer the release trusts and the env setting that lost it. No kubectl
	// stub: this arm never reads the key Secret, and its absence is the point.
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

// seedMCPAuthPublicKey writes a PEM public key inside the scenario's temp tree so
// the path normalizes in goldens.
func seedMCPAuthPublicKey(t *testing.T, setup env.Setup) string {
	t.Helper()
	keyPath := filepath.Join(setup.Home, "desktopid.pub")
	mustWriteFile(t, keyPath, "-----BEGIN PUBLIC KEY-----\nMCowBQYDK2VwAyEAtestkeytestkeytestkeytestkeytestkeytestke=\n-----END PUBLIC KEY-----\n")
	return keyPath
}
