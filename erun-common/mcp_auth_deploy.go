package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// resolveMCPAuthPublicKey loads the desktop key the per-env MCP edge verifies
// against. A blank path is the default: no MCP auth, so the edge stays
// loopback-only (ok is false).
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

// applyMCPAuthToRuntimeSpec applies MCP-auth metadata only to the runtime
// release: the erun-mcp container ships in the erun-devops runtime chart, so
// backend component charts never carry mcpAuth. Auth is sticky — a redeploy that
// does not re-supply the key rethreads the env's persisted one — and turning it
// off is explicit, so a routine version bump can never quietly downgrade a
// publicly-reachable edge to unauthenticated.
func applyMCPAuthToRuntimeSpec(ctx Context, target DeployTarget, spec *DeploySpec) error {
	if spec == nil || spec.Deploy.ReleaseName != RuntimeReleaseName(target.Tenant) {
		return nil
	}
	keyPath := resolveMCPAuthKeyPathForDeploy(ctx, target, spec.Target.EnvConfig)
	pem, ok, err := resolveMCPAuthPublicKey(keyPath)
	if err != nil {
		return err
	}
	if ok {
		// Desktop path: a local public key → trust a file:// issuer.
		applyMCPAuthDeployMetadata(&spec.Deploy, pem, keyPath)
		return nil
	}
	if !target.DisableMCPAuth {
		// Hosted path: trust the tenant's registered OIDC issuer (https://). No
		// local key — the MCP edge fetches the issuer's JWKS. Mutually exclusive
		// with the desktop key above; empty issuer leaves the deploy
		// unauthenticated.
		applyMCPAuthOIDCMetadata(&spec.Deploy, spec.Target.EnvConfig.MCPAuthIssuer)
	}
	return nil
}

// resolveMCPAuthKeyPathForDeploy picks the desktop public key this deploy trusts.
// Precedence: the explicit opt-out clears it, then the caller-supplied path, then
// the path the env recorded when auth was last enabled. Rethreading the recorded
// path is what makes auth survive a plain version bump.
func resolveMCPAuthKeyPathForDeploy(ctx Context, target DeployTarget, envConfig EnvConfig) string {
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

// guardMCPAuthDowngrade refuses a deploy that would strip authentication from an
// env whose live release still has it enabled. The env's MCP edge executes
// arbitrary commands in the pod, so an unnoticed downgrade is the failure mode
// worth a hard stop; the operator opts out explicitly instead.
//
// The live read is the only way to catch an env that enabled auth before the
// setting was recorded in its config, and it only runs on the risky path (a
// runtime deploy whose resolved plan has auth off), so an auth-enabled deploy
// pays nothing. Scoped to explicit deploy requests: `erun open`'s heal-redeploy
// rethreads a recorded key but must not be blocked from handing over a shell.
func guardMCPAuthDowngrade(ctx Context, target DeployTarget, deployInput HelmDeploySpec) error {
	if target.DisableMCPAuth || deployInput.MCPAuthEnabled {
		return nil
	}
	if deployInput.ReleaseName != RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	enabled, known := liveMCPAuthEnabled(deployInput)
	if !known {
		return nil
	}
	if !enabled {
		ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has none; leaving the MCP edge loopback-only")
		return nil
	}
	ctx.Trace("deploy: mcp auth: release " + deployInput.ReleaseName + " has it enabled but this deploy resolved none; refusing to downgrade")
	return fmt.Errorf("deploy %s/%s: MCP auth is enabled on the live %s release, but this deploy resolved none — it would leave the environment's MCP edge unauthenticated. Re-supply the key with --mcp-auth-public-key <path>, or pass --no-mcp-auth to turn authentication off on purpose",
		deployInput.Tenant, deployInput.Environment, deployInput.ReleaseName)
}

// mcpAuthLiveProbeOverrideEnv is a test-only seam that answers "does the live
// release have MCP auth enabled?" from a static value instead of reading helm, so
// integration goldens never depend on a real cluster's releases. Not a production
// knob: when the variable is unset the probe performs the real helm read.
// Accepted values: "enabled", "disabled"; anything else (including empty) means
// unknown, which is how a machine with no reachable release answers.
const mcpAuthLiveProbeOverrideEnv = "ERUN_MCP_AUTH_LIVE_PROBE_OVERRIDE"

// liveMCPAuthEnabled reports whether the deployed release currently has MCP auth
// on. known=false means the release could not be read (absent, no helm, no
// cluster) — the caller then imposes no constraint rather than block a deploy on
// an unreachable cluster.
func liveMCPAuthEnabled(deployInput HelmDeploySpec) (enabled bool, known bool) {
	if override, ok := os.LookupEnv(mcpAuthLiveProbeOverrideEnv); ok {
		switch strings.TrimSpace(override) {
		case "enabled":
			return true, true
		case "disabled":
			return false, true
		default:
			return false, false
		}
	}
	release := strings.TrimSpace(deployInput.ReleaseName)
	namespace := strings.TrimSpace(deployInput.Namespace)
	if release == "" || namespace == "" {
		return false, false
	}
	args := []string{"get", "values", release, "--namespace", namespace, "-o", "json"}
	if kubernetesContext := strings.TrimSpace(deployInput.KubernetesContext); kubernetesContext != "" {
		args = append(args, "--kube-context", kubernetesContext)
	}
	output, err := Command("helm", args...).Output()
	if err != nil {
		return false, false
	}
	var values struct {
		MCPAuth struct {
			Enabled bool `json:"enabled"`
		} `json:"mcpAuth"`
	}
	if err := json.Unmarshal(output, &values); err != nil {
		return false, false
	}
	return values.MCPAuth.Enabled, true
}

// applyMCPAuthOIDCMetadata configures the runtime MCP edge to trust the tenant's
// registered OIDC issuer, with the per-env audience from the shared convention.
// No Secret is applied (the edge verifies against the issuer's published JWKS),
// which is why the chart mounts the desktop key only when a secretName is set.
// A blank issuer is a no-op, leaving a non-desktop deploy unauthenticated.
func applyMCPAuthOIDCMetadata(deployInput *HelmDeploySpec, issuer string) {
	issuer = strings.TrimSpace(issuer)
	if deployInput == nil || issuer == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthIssuer = issuer
	deployInput.MCPAuthAudience = MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
}

// mcpAuthSecretName derives the per-release Secret that carries the desktop
// public key the env's erun-mcp edge verifies bearer tokens against.
func mcpAuthSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-mcp-auth"
}

func mcpAuthSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// renderMCPAuthSecret writes the desktop public key under the filename the
// chart mounts and the file:// issuer both reference, so all three agree. The
// key is public but rides in a Secret to reuse the Cloudflare token's apply path.
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
`, name, namespace, desktopMCPPublicKeyFile, publicKeyPEM)
}

// applyMCPAuthSecret delivers the desktop public key into the env's runtime
// pod. A no-op unless the deploy injected a trusted key (a desktop deploy); the
// manifest is piped via stdin so it never appears as an argv element.
func applyMCPAuthSecret(ctx Context, deployInput HelmDeploySpec) error {
	if !deployInput.MCPAuthEnabled || strings.TrimSpace(deployInput.MCPAuthSecretName) == "" {
		return nil
	}
	args := mcpAuthSecretApplyArgs(deployInput.Namespace, deployInput.KubernetesContext)
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
	cmd := Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply mcp auth secret: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyMCPAuthDeployMetadata records the MCP-auth fields but applies no Secret
// — that happens later in applyMCPAuthSecret. An empty key is a no-op, leaving
// a non-desktop deploy unauthenticated (back-compat). Issuer and audience come
// from the shared conventions so signer, chart, and verifier all agree. The key
// path rides along so the deploy can record it on the env and rethread it next
// time.
func applyMCPAuthDeployMetadata(deployInput *HelmDeploySpec, publicKeyPEM, publicKeyPath string) {
	if deployInput == nil || strings.TrimSpace(publicKeyPEM) == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthPublicKeyPEM = publicKeyPEM
	deployInput.MCPAuthPublicKeyPath = strings.TrimSpace(publicKeyPath)
	deployInput.MCPAuthIssuer = DesktopMCPIssuer()
	deployInput.MCPAuthAudience = MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
	deployInput.MCPAuthSecretName = mcpAuthSecretName(deployInput.ReleaseName)
}
