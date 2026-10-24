package deploy

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
)

// resolveMCPAuthPublicKey returns ok=false for a blank path: no auth, edge stays loopback-only.
func resolveMCPAuthPublicKey(path string) (string, bool, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false, nil
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return "", false, fmt.Errorf("read mcp auth public key %s: %w", path, err)
	}
	return string(pem), true, nil
}

// ApplyMCPAuthToRuntimeSpec applies MCP auth to the runtime release only, which
// serves the edge. Auth is sticky and disabling it is explicit, so a version bump
// cannot quietly downgrade a public edge.
func ApplyMCPAuthToRuntimeSpec(ctx core.Context, target model.DeployTarget, spec *seam.DeploySpec) error {
	if spec == nil || spec.Deploy.ReleaseName != config.RuntimeReleaseName(target.Tenant) {
		return nil
	}
	keyPath := resolveMCPAuthKeyPathForDeploy(ctx, target, spec.Target.EnvConfig)
	pem, ok, err := resolveMCPAuthPublicKey(keyPath)
	if err != nil {
		return err
	}
	if ok {
		applyMCPAuthDeployMetadata(&spec.Deploy, pem, keyPath)
	}
	return nil
}

// Rethreading the recorded path is what makes auth survive a plain version bump.
func resolveMCPAuthKeyPathForDeploy(ctx core.Context, target model.DeployTarget, envConfig config.EnvConfig) string {
	if target.DisableMCPAuth {
		ctx.Trace("deploy: mcp auth disabled by request; the env's MCP edge will accept loopback traffic only")
		return ""
	}
	if path := strings.TrimSpace(target.MCPAuthPublicKeyPath); path != "" {
		return path
	}
	if path := strings.TrimSpace(envConfig.MCPAuthPublicKeyPath); path != "" {
		ctx.Trace("deploy: mcp auth: rethreading the env's recorded public key " + path)
		return path
	}
	return ""
}

// recordMCPAuthKeyOnEnv records the key when helm receives it, not after rollout
// succeeds, so a failed rollout still leaves a key the downgrade guard can rethread.
func recordMCPAuthKeyOnEnv(ctx core.Context, deployInput seam.HelmDeploySpec) error {
	keyPath := strings.TrimSpace(deployInput.MCPAuthPublicKeyPath)
	tenant := strings.TrimSpace(deployInput.Tenant)
	environment := strings.TrimSpace(deployInput.Environment)
	if !deployInput.MCPAuthEnabled || keyPath == "" || tenant == "" || environment == "" {
		return nil
	}
	envConfig, _, err := config.LoadEnvConfig(tenant, environment)
	loaded := err == nil
	if loaded && strings.TrimSpace(envConfig.MCPAuthPublicKeyPath) == keyPath {
		return nil
	}
	ctx.Trace("deploy: mcp auth: recording the public key " + keyPath + " on " + tenant + "/" + environment)
	if ctx.DryRun {
		return nil
	}
	if !loaded {
		// init writes the env config around its own first rollout, so an unreadable one is not fatal here.
		ctx.Trace("deploy: mcp auth: could not read the env config of " + tenant + "/" + environment + " to record the public key")
		return nil
	}
	envConfig.MCPAuthPublicKeyPath = keyPath
	if err := config.SaveEnvConfig(tenant, envConfig); err != nil {
		return fmt.Errorf("record mcp auth public key on %s/%s: %w", tenant, environment, err)
	}
	return nil
}

// guardMCPAuthDowngrade refuses stripping auth from a live authenticated edge,
// which executes arbitrary commands in the pod. It reads the live release only on
// the risky path; `erun open`'s heal-redeploy is exempt.
func guardMCPAuthDowngrade(ctx core.Context, target model.DeployTarget, deployInput seam.HelmDeploySpec) error {
	if target.DisableMCPAuth || deployInput.MCPAuthEnabled {
		return nil
	}
	if deployInput.ReleaseName != config.RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	live, known := readLiveMCPAuth(deployInput)
	if !known {
		return nil
	}
	if !live.enabled {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has none; leaving the MCP edge loopback-only")
		return nil
	}
	ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has it enabled but this deploy resolved none; refusing to downgrade")
	return fmt.Errorf("deploy %s/%s: MCP auth is enabled on the live %s release, but this deploy resolved none — it would leave the environment's MCP edge unauthenticated. %s",
		deployInput.Tenant, deployInput.Environment, deployInput.ReleaseName, mcpAuthDowngradeRecovery(ctx, deployInput, live))
}

// mcpAuthDowngradeRecovery names what the live release trusts; an OIDC edge has no
// local key to ask for.
func mcpAuthDowngradeRecovery(ctx core.Context, deployInput seam.HelmDeploySpec, live liveMCPAuth) string {
	const optOut = ", or pass --no-mcp-auth to turn authentication off on purpose"
	if issuer := strings.TrimSpace(live.issuer); issuer != "" && !strings.HasPrefix(issuer, mcpauth.FileIssuerScheme) {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " authenticates against the OIDC issuer " + issuer + "; no local key is involved")
		return "The release authenticates against the OIDC issuer " + issuer + ", not a local key; erun has no supported way to configure that issuer, so switch the release to the key-based path with --mcp-auth-public-key <path>" + optOut
	}
	secret := strings.TrimSpace(live.secretName)
	if secret == "" {
		secret = mcpAuthSecretName(deployInput.ReleaseName)
	}
	trusted, ok := readLiveMCPAuthPublicKey(ctx, deployInput, secret)
	if !ok {
		return "The release trusts a desktop public key held in secret " + secret + ", which could not be read from here; re-supply that key with --mcp-auth-public-key <path>" + optOut
	}
	fingerprint := publicKeyFingerprint(trusted)
	if path, matches := desktopIdentityPublicKeyMatching(trusted); matches {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " trusts this host's desktop identity key " + path)
		return "The release trusts the desktop identity key on this host, " + path + " (sha256 " + fingerprint + "); re-supply it with --mcp-auth-public-key " + path + optOut
	}
	return "The release trusts the desktop public key in secret " + secret + " (sha256 " + fingerprint + "), which is not this host's desktop identity key; re-supply the matching key with --mcp-auth-public-key <path>" + optOut
}

// readLiveMCPAuthPublicKey returns ok=false on any failure, so cluster errors stay
// out of the operator-facing message.
func readLiveMCPAuthPublicKey(ctx core.Context, deployInput seam.HelmDeploySpec, secret string) ([]byte, bool) {
	namespace := strings.TrimSpace(deployInput.Namespace)
	if secret == "" || namespace == "" {
		return nil, false
	}
	ctx.Trace("deploy: mcp auth: reading the key release " + deployInput.ReleaseName + " trusts from secret " + secret)
	args := []string{}
	if kubernetesContext := strings.TrimSpace(deployInput.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--context", kubernetesContext)
	}
	jsonPath := "jsonpath={.data." + strings.ReplaceAll(mcpauth.DesktopMCPPublicKeyFile, ".", `\.`) + "}"
	args = append(args, "-n", namespace, "get", "secret", secret, "-o", jsonPath)
	output, err := core.Command("kubectl", args...).Output()
	if err != nil {
		ctx.Trace("deploy: mcp auth: secret " + secret + " could not be read")
		return nil, false
	}
	pem, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(output)))
	if err != nil || len(bytes.TrimSpace(pem)) == 0 {
		ctx.Trace("deploy: mcp auth: secret " + secret + " holds no readable public key")
		return nil, false
	}
	return pem, true
}

func desktopIdentityPublicKeyMatching(trusted []byte) (string, bool) {
	dir := strings.TrimSpace(core.DefaultDesktopIdentityDir())
	if dir == "" {
		return "", false
	}
	path := mcpauth.DesktopIdentityPublicKeyPath(dir)
	local, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if !bytes.Equal(bytes.TrimSpace(local), bytes.TrimSpace(trusted)) {
		return "", false
	}
	return path, true
}

func publicKeyFingerprint(pem []byte) string {
	sum := sha256.Sum256(bytes.TrimSpace(pem))
	return hex.EncodeToString(sum[:])
}

// mcpAuthLiveProbeOverrideEnv is a test-only seam replacing the live helm read:
// "disabled", "enabled|<issuer>|<secret>", else unknown.
const mcpAuthLiveProbeOverrideEnv = "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE"

type liveMCPAuth struct {
	enabled    bool
	issuer     string
	secretName string
}

// readLiveMCPAuth returns known=false when the release cannot be read, and the
// caller then imposes no constraint.
func readLiveMCPAuth(deployInput seam.HelmDeploySpec) (liveMCPAuth, bool) {
	if override, ok := os.LookupEnv(mcpAuthLiveProbeOverrideEnv); ok {
		return parseLiveMCPAuthOverride(override)
	}
	release := strings.TrimSpace(deployInput.ReleaseName)
	namespace := strings.TrimSpace(deployInput.Namespace)
	if release == "" || namespace == "" {
		return liveMCPAuth{}, false
	}
	args := []string{"get", "values", release, "--namespace", namespace, "-o", "json"}
	if kubernetesContext := strings.TrimSpace(deployInput.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--kube-context", kubernetesContext)
	}
	output, err := core.Command("helm", args...).Output()
	if err != nil {
		return liveMCPAuth{}, false
	}
	var values struct {
		MCPAuth struct {
			Enabled    bool   `json:"enabled"`
			Issuer     string `json:"issuer"`
			SecretName string `json:"secretName"`
		} `json:"mcpAuth"`
	}
	if err := json.Unmarshal(output, &values); err != nil {
		return liveMCPAuth{}, false
	}
	return liveMCPAuth{
		enabled:    values.MCPAuth.Enabled,
		issuer:     strings.TrimSpace(values.MCPAuth.Issuer),
		secretName: strings.TrimSpace(values.MCPAuth.SecretName),
	}, true
}

func parseLiveMCPAuthOverride(override string) (liveMCPAuth, bool) {
	fields := strings.Split(strings.TrimSpace(override), "|")
	switch strings.TrimSpace(fields[0]) {
	case "enabled":
		live := liveMCPAuth{enabled: true}
		if len(fields) > 1 {
			live.issuer = strings.TrimSpace(fields[1])
		}
		if len(fields) > 2 {
			live.secretName = strings.TrimSpace(fields[2])
		}
		return live, true
	case "disabled":
		return liveMCPAuth{}, true
	default:
		return liveMCPAuth{}, false
	}
}

func mcpAuthSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-mcp-auth"
}

// The key is public but rides in a Secret to reuse the Cloudflare token's apply path.
func renderMCPAuthSecret(name, namespace, publicKeyPEM string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-deploy
type: Opaque
stringData:
  %s: %q
`, name, namespace, mcpauth.DesktopMCPPublicKeyFile, publicKeyPEM)
}

// applyMCPAuthSecret pipes the manifest via stdin so the key never appears in argv.
func applyMCPAuthSecret(ctx core.Context, deployInput seam.HelmDeploySpec) error {
	if !deployInput.MCPAuthEnabled || strings.TrimSpace(deployInput.MCPAuthSecretName) == "" {
		return nil
	}
	args := kube.KubectlApplyStdinArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply mcp auth public key secret " + deployInput.MCPAuthSecretName)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if strings.TrimSpace(deployInput.MCPAuthPublicKeyPEM) == "" {
		return fmt.Errorf("deploy %s: mcp auth enabled but no public key provided", deployInput.ReleaseName)
	}
	manifest := renderMCPAuthSecret(
		deployInput.MCPAuthSecretName,
		deployInput.Namespace,
		deployInput.MCPAuthPublicKeyPEM,
	)
	return kube.ApplySecretManifest(deployInput.KubernetesContext, deployInput.Namespace, "mcp auth secret", manifest, args)
}

// applyMCPAuthDeployMetadata records MCP-auth fields; the Secret is applied later
// by applyMCPAuthSecret. An empty key leaves the deploy unauthenticated.
func applyMCPAuthDeployMetadata(deployInput *seam.HelmDeploySpec, publicKeyPEM, publicKeyPath string) {
	if deployInput == nil || strings.TrimSpace(publicKeyPEM) == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthPublicKeyPEM = publicKeyPEM
	deployInput.MCPAuthPublicKeyPath = strings.TrimSpace(publicKeyPath)
	deployInput.MCPAuthIssuer = mcpauth.DesktopMCPIssuer()
	deployInput.MCPAuthAudience = mcpauth.MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
	deployInput.MCPAuthSecretName = mcpAuthSecretName(deployInput.ReleaseName)
}
