package integration

import (
	"encoding/base64"
	"fmt"
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

func testDeployRealRunRefreshesImagePullSecretViaStubbedKubectl(t *testing.T) {
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")

	dockerCfgDir, encoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:s3cret-token")

	stubs := setup.Cwd + "/stubs"
	// The refresh reads the Secret before applying, so "get secret" answers NotFound.
	fixture.StubBinaryWithScript(t, stubs, "kubectl", `case "$*" in
  *"get secret"*) echo 'Error from server (NotFound): secrets "ecr-pull" not found' >&2; exit 1 ;;
  *) exit 0 ;;
esac
`)
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_refreshes_image_pull_secret_via_stubbed_kubectl", normalize.Apply(result.Combined))
}

func testDeployRealRunImagePullSecretMergePreservesUnresolvedHost(t *testing.T) {
	// An unresolvable existing host must survive untouched, and an unresolvable new
	// host gets no placeholder. The applied manifest is captured to a file.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - regcred\nruntimeimage: neverresolves.example/team-devops\n")

	dockerCfgDir, freshEncoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:fresh-token")

	existingSecretJSON := fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`,
		base64.StdEncoding.EncodeToString([]byte(`{"auths":{"registry.example":{"auth":"stale-value"},"legacy-other.example":{"auth":"preserved-value"}}}`)))
	appliedManifestPath := filepath.Join(setup.Cwd, "applied-regcred.yaml")

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", fmt.Sprintf(`case "$*" in
  *"get secret regcred -o json"*) cat <<'JSON'
%s
JSON
    ;;
  *"apply -f -"*) cat > %q ;;
  *) exit 0 ;;
esac
`, existingSecretJSON, appliedManifestPath))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "fresh-token") || strings.Contains(result.Combined, freshEncoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}

	assertMergedPullSecretManifest(t, appliedManifestPath, freshEncoded)
	golden.Equal(t, "deploy/real_run_image_pull_secret_merge_preserves_unresolved_host", normalize.Apply(result.Combined))
}

func assertMergedPullSecretManifest(t *testing.T, appliedManifestPath, freshEncoded string) {
	t.Helper()
	applied, err := os.ReadFile(appliedManifestPath)
	if err != nil {
		t.Fatalf("read applied manifest: %v", err)
	}
	manifest := string(applied)
	if !strings.Contains(manifest, freshEncoded) {
		t.Fatalf("manifest missing the refreshed registry.example credential:\n%s", manifest)
	}
	if strings.Contains(manifest, "stale-value") {
		t.Fatalf("manifest still carries the stale registry.example credential it should have refreshed:\n%s", manifest)
	}
	if !strings.Contains(manifest, "preserved-value") {
		t.Fatalf("manifest lost the unresolved legacy-other.example host's existing credential:\n%s", manifest)
	}
	if strings.Contains(manifest, "neverresolves.example") {
		t.Fatalf("manifest carries a placeholder entry for a host that never resolved and never had one:\n%s", manifest)
	}
}

func testDeployRealRunImagePullSecretRefusesMalformedExistingSecret(t *testing.T) {
	// An undecodable Secret is refused rather than rebuilt from the resolved subset.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - regcred\n")

	// A host must resolve or the Secret is never read.
	dockerCfgDir, encoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:s3cret-token")

	malformedSecretJSON := fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`,
		base64.StdEncoding.EncodeToString([]byte("not valid json")))

	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryWithScript(t, stubs, "kubectl", fmt.Sprintf(`case "$*" in
  *"get secret regcred -o json"*) cat <<'JSON'
%s
JSON
    ;;
  *"apply -f -"*) echo "apply must not run against an undecodable secret" >&2; exit 1 ;;
  *) exit 0 ;;
esac
`, malformedSecretJSON))
	fixture.StubBinary(t, stubs, "helm", "")
	fixture.StubBinary(t, stubs, "docker", "")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a non-zero exit for an undecodable existing secret, got 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_image_pull_secret_refuses_malformed_existing_secret", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvImagePullSecrets(t *testing.T) {
	// Pull secrets are re-scoped under the erun-devops subchart key.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ghcr-pull\n")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0")
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secrets", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvImagePullSecretRefreshedFromHostCredential(t *testing.T) {
	// ECR tokens expire after 12h, so deploy re-mints a named Secret from host
	// credentials. DOCKER_CONFIG isolates the host resolver.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\n")

	dockerCfgDir, encoded := seedInlineDockerAuth(t, setup, "registry.example", "AWS:s3cret-token")

	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secret_refreshed_from_host_credential", normalize.Apply(result.Combined))
}

func testDeployDryRunRemoteEnvImagePullSecretCoversRuntimeImageOverrideRegistry(t *testing.T) {
	// The pull secret must cover the runtimeimage's own registry, which here is
	// the only one with a host credential.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ecr-pull\nruntimeimage: otherregistry.example/team-devops\n")

	dockerCfgDir, encoded := seedInlineDockerAuth(t, setup, "otherregistry.example", "AWS:s3cret-token")

	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0", "DOCKER_CONFIG="+dockerCfgDir)
	result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	if strings.Contains(result.Combined, "s3cret-token") || strings.Contains(result.Combined, encoded) {
		t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_remote_env_image_pull_secret_covers_runtime_image_override_registry", normalize.Apply(result.Combined))
}

func testDeployDryRunPrivateGHCRRuntimeImageWithNoHostCredentialTracesPullSecretDecision(t *testing.T) {
	// A non-erun tenant's image defaults onto ghcr.io, so --dry-run must report pullability.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	envVars := append(setup.Env(), "ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*")
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.86", "--dry-run"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "deploy/dry_run_private_ghcr_runtime_image_with_no_host_credential_traces_pull_secret_decision", normalize.Apply(result.Combined))
}

func testDeployRealRunRefusesBeforeRolloutWhenPrivateRuntimeImageHasNoPullCredential(t *testing.T) {
	// Recreate tears down the running pod first, so an unpullable image must be
	// refused before any cluster call. No kubectl/helm stub is declared.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*",
		// Empty: no image counts as anonymously pullable.
		"ERUN_ANONYMOUS_PULLABLE_OVERRIDE=",
	)
	result := erun.Run(t, []string{"deploy", "frs", "build", "--version", "1.0.86"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
	if result.ExitCode == 0 {
		t.Fatalf("expected a refusal for a private runtime image with no resolvable pull credential, got exit 0:\n%s", result.Combined)
	}
	if strings.Contains(result.Combined, "kubectl") || strings.Contains(result.Combined, "helm upgrade") {
		t.Fatalf("refusal must fire before any cluster interaction: %s", result.Combined)
	}
	golden.Equal(t, "deploy/real_run_refuses_before_rollout_when_private_runtime_image_has_no_pull_credential", normalize.Apply(result.Combined))
}

// TestDeployInPodResolvesDeclaredImagePullSecret: in-pod, the credential comes from
// the env's imagepullsecrets Secret; the runtime-chart guard then refuses the
// rollout, so the in-pod leg ends at the chart read.
func TestDeployInPodResolvesDeclaredImagePullSecret(t *testing.T) {
	t.Parallel()

	// run differs only in whether the process is marked as team/dev's own pod.
	run := func(t *testing.T, inPod bool) string {
		t.Helper()
		setup := env.New(t)
		fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
		envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
		existing, err := os.ReadFile(envConfigPath)
		if err != nil {
			t.Fatalf("read env config: %v", err)
		}
		mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - ghcr-pull\n")

		secretJSON := fmt.Sprintf(`{"data":{".dockerconfigjson":%q}}`,
			base64.StdEncoding.EncodeToString([]byte(`{"auths":{"ghcr.io":{"auth":"cHVsbHNlY3JldDpwdw=="}}}`)))

		stubs := setup.Cwd + "/stubs"
		fixture.StubBinaryWithScript(t, stubs, "kubectl", fmt.Sprintf(`case "$*" in
  *"get secret ghcr-pull -o json"*) cat <<'JSON'
%s
JSON
    ;;
  *) exit 0 ;;
esac
`, secretJSON))
		fixture.StubBinary(t, stubs, "helm", "")
		fixture.StubBinary(t, stubs, "docker", "")

		envVars := append(setup.Env(),
			"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=team-devops:1.0.0",
			"DOCKER_CONFIG="+t.TempDir())
		if inPod {
			envVars = append(envVars, "ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_NAMESPACE=team-dev")
		}
		envVars = append(envVars, fixture.StubEnv(stubs, "kubectl", "helm", "docker")...)

		result := erun.Run(t, []string{"deploy", "team", "dev", "--version", "1.0.0"}, erun.RunOptions{Cwd: setup.Home, Env: envVars})
		if !inPod && result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if strings.Contains(result.Combined, "pullsecret") {
			t.Fatalf("the resolved credential must never appear in trace output: %s", result.Combined)
		}
		return result.Combined
	}

	const resolved = "registry credential: resolved 1 registry credential(s) from this environment's declared image pull secret(s) ghcr-pull"
	const authenticatedRead = "runtime chart team-devops 1.0.0 found in registry.example/test"

	inPodTrace := run(t, true)
	if !strings.Contains(inPodTrace, resolved) {
		t.Fatalf("in-pod deploy did not resolve the env's declared image pull secret:\n%s", inPodTrace)
	}
	if !strings.Contains(inPodTrace, authenticatedRead) {
		t.Fatalf("the resolved credential did not carry the tenant's private chart read:\n%s", inPodTrace)
	}
	if !strings.Contains(inPodTrace, "refusing to deploy the runtime chart from inside this environment's own pod") {
		t.Fatalf("the in-pod leg must stop at the in-pod runtime chart guard, not some other failure:\n%s", inPodTrace)
	}
	if trace := run(t, false); strings.Contains(trace, resolved) {
		t.Fatalf("a deploy outside its env's runtime pod must not read that env's declared pull secrets:\n%s", trace)
	}
}

func seedInlineDockerAuth(t *testing.T, setup env.Setup, registry, credential string) (string, string) {
	t.Helper()
	dockerCfgDir := filepath.Join(setup.Cwd, "docker-inline")
	if err := os.MkdirAll(dockerCfgDir, 0o755); err != nil {
		t.Fatalf("mkdir docker config dir: %v", err)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(credential))
	dockerCfg := fmt.Sprintf(`{"auths":{%q:{"auth":%q}}}`, registry, encoded)
	if err := os.WriteFile(filepath.Join(dockerCfgDir, "config.json"), []byte(dockerCfg), 0o644); err != nil {
		t.Fatalf("write docker config: %v", err)
	}
	return dockerCfgDir, encoded
}
