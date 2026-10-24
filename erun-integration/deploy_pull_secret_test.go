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
	// Real-run counterpart of the dry-run image-pull-secret scenarios:
	// proves the kubectl apply actually executes (Command + stdin
	// manifest + CombinedOutput), not just that dry-run traces it. The
	// kubectl stub exits 0 so the apply — and the rollout after it —
	// both succeed.
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
	// The refresh reads the Secret back before applying (to merge rather
	// than replace its auths), so the generic empty-output kubectl stub
	// every other kubectl call here still uses must answer "get secret"
	// specifically: NotFound, since ecr-pull has never been created in
	// this fixture.
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
	// The core of the merge fix: a Secret already covering two hosts,
	// redeployed on a host that can only resolve one of them, must refresh
	// the resolvable host and leave the other byte-for-byte untouched --
	// not lose it to a full-document rewrite. A third host that is in play
	// this run (via runtimeimage) but has never had an entry and does not
	// resolve must get no placeholder auth. The applied manifest is
	// captured to a file (a side effect outside erun.Run's captured
	// streams) so the test can assert on the actual merged
	// .dockerconfigjson rather than the trace alone.
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

// assertMergedPullSecretManifest checks the applied regcred manifest refreshed
// the resolvable host, kept the unresolved one, and invented no placeholder.
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
	// The other design decision the merge fix makes explicit: an existing
	// Secret that cannot be decoded is refused rather than silently
	// rebuilt from the resolved subset alone, since guessing wrong there
	// would destroy exactly the coverage the merge exists to protect.
	setup := env.New(t)
	fixture.SeedRemoteRepoPathTenantEnv(t, setup, "team", "dev", "/nonexistent-remote/team")
	envConfigPath := filepath.Join(setup.ConfigHome, "erun", "team", "dev", "config.yaml")
	existing, err := os.ReadFile(envConfigPath)
	if err != nil {
		t.Fatalf("read env config: %v", err)
	}
	mustWriteFile(t, envConfigPath, string(existing)+"imagepullsecrets:\n    - regcred\n")

	// A host must actually resolve this run, or refreshImagePullSecrets
	// never reaches the Secret read at all (nothing to overlay).
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
	// A tenant umbrella image can be a private ghcr package. The env's
	// imagepullsecrets list rides into the runtime deploy as
	// imagePullSecrets[i].name, re-scoped under the erun-devops subchart key so
	// the runtime pod authenticates the pull. Orthogonal to the umbrella's
	// own-image default (both appear); an env with no list emits nothing.
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
	// An ECR authorization token expires after twelve hours (#1256), so a
	// pull secret named once via --image-pull-secret and never refreshed
	// eventually rots. When the host running `erun deploy` already has a
	// credential for the deploy registry (docker config, or the AWS CLI
	// for ECR), deploy re-mints the named Secret from it instead of
	// leaving whatever it held days ago. DOCKER_CONFIG points the host
	// resolver at this isolated dir, the same seam the init registry
	// credential scenario uses; the fixture's containerregistry
	// (registry.example/test) is deliberately addressed here.
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
	// #1328: a runtimeimage pinned to a registry other than the env's
	// containerRegistry (e.g. built into one registry, deployed from
	// another) left the refreshed pull secret uncovered for the registry
	// the pod actually pulls from — before this fix, resolution only ever
	// tried containerRegistry's host, so a credential known for the
	// image's own registry was never even attempted. Here DOCKER_CONFIG
	// carries a credential for the runtimeimage's own registry
	// (otherregistry.example) but NOT for the fixture's containerregistry
	// (registry.example): before the fix this applied no secret at all
	// ("no host credential resolved for registry.example; leaving
	// ecr-pull untouched" and nothing else tried); after the fix the
	// image's own registry still resolves and the secret is applied.
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
	// A runtime-only env's image defaults onto ghcr.io/sophium/<tenant>-devops
	// (DefaultContainerRegistry) the moment it is not the erun product's own
	// tenant, with no operator action needed -- so --dry-run must show whether
	// that image can even be pulled instead of staying silent about it, the
	// one moment the operator could still fix a missing credential cheaply.
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
	// The severity this preflight exists for: the runtime chart's Recreate
	// strategy tears down the running pod before the new one is scheduled, so
	// discovering an unpullable image only after the rollout starts takes the
	// environment down rather than leaving a stalled rollout beside a healthy
	// pod. No kubectl/helm stub is declared at all -- if the refusal did not
	// fire before any cluster interaction, the command would instead fail with
	// "executable file not found", which would show up in the golden below.
	setup := env.New(t)
	fixture.SeedRuntimeTenantEnvNoRepoPath(t, setup, "frs", "build")
	envVars := append(setup.Env(),
		"ERUN_PUBLISHED_CHART_PROBE_OVERRIDE=erun-devops:*",
		// Absent from the override, an anonymous-pull probe of this specific
		// image resolves to "not pullable" (anonymousPullableOverrideEnv).
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

// A deploy running inside the target env's own runtime pod has none of the
// three credential routes resolveGHCRBasicAuth knows -- no docker config, no gh
// session, no GH_TOKEN/GITHUB_TOKEN -- so before this fix every read of the
// tenant's private chart was anonymous, ghcr refused to mint a token, and deploy
// refused the read as unconfirmed. The env already declares where the credential
// lives: its imagepullsecrets names a dockerconfigjson Secret in its own
// namespace that the pod's service account can read. The pod resolves it from
// there instead of falling back to anonymous.
//
// The in-pod runtime-chart guard refuses the rollout itself once the resolve is
// done, so the in-pod leg ends at the authenticated chart read rather than at a
// completed deploy; both are asserted below, so the leg still proves the
// credential carried the read.
func TestDeployInPodResolvesDeclaredImagePullSecret(t *testing.T) {
	t.Parallel()

	// run deploys team/dev from a credential-less process, optionally marking it
	// as team/dev's own runtime pod. Everything else is identical, so the trace
	// difference is the gating and nothing else.
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

		// The pod pulls with the credential the Secret declares; the process
		// holds no docker config of its own, so only the declared Secret can
		// supply one.
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
	// The read the credential exists for: the tenant's private chart probe
	// authenticates instead of being refused as an anonymous read. The in-pod
	// runtime-chart guard stops the rollout after the resolve, so this read — not
	// a completed deploy — is where the in-pod leg ends.
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

// seedInlineDockerAuth writes a docker config carrying an inline auth for
// registry and returns its directory and the encoded credential.
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
