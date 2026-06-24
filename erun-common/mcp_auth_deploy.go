package eruncommon

import (
	"fmt"
	"os"
	"strings"
)

// resolveMCPAuthPublicKey reads the PEM public key the deploy will trust the
// per-env MCP edge against (issue #655). A blank path means no MCP auth — the
// default; the edge stays loopback-only — so ok is false. A non-empty path that
// cannot be read is a hard error.
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

// applyMCPAuthToRuntimeSpec injects MCP-auth metadata onto the runtime chart's
// spec only — the erun-mcp container lives in the erun-devops runtime release,
// so backend component charts never receive mcpAuth values. It reads the public
// key from target.MCPAuthPublicKeyPath; a blank path is a no-op, leaving a
// non-desktop deploy unauthenticated (back-compat).
func applyMCPAuthToRuntimeSpec(target DeployTarget, spec *DeploySpec) error {
	if spec == nil || spec.Deploy.ReleaseName != RuntimeReleaseName(target.Tenant) {
		return nil
	}
	pem, ok, err := resolveMCPAuthPublicKey(target.MCPAuthPublicKeyPath)
	if err != nil {
		return err
	}
	if ok {
		applyMCPAuthDeployMetadata(&spec.Deploy, pem)
	}
	return nil
}

// mcpAuthSecretName derives the per-release Secret that carries the desktop
// public key the env's erun-mcp edge verifies bearer tokens against (#655).
func mcpAuthSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-mcp-auth"
}

// mcpAuthSecretApplyArgs builds the kubectl argv that applies the MCP-auth
// public-key Secret, reading the manifest from stdin (`-f -`). Shared by the
// dry-run trace and the live apply.
func mcpAuthSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// renderMCPAuthSecret renders the Secret manifest carrying the desktop public
// key under the desktopid.pub key — the same filename the chart mounts and the
// file:// issuer references. The key is public, but it rides in a Secret so the
// out-of-band apply reuses the same mechanics as the Cloudflare token.
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

// applyMCPAuthSecret creates or updates the Secret that delivers the desktop
// public key into the env's runtime pod, piped to `kubectl apply -f -` via
// stdin so the manifest is never an argv element. It is a no-op unless the
// deploy injected a trusted key (a desktop deploy); dry-run traces the apply
// without touching the cluster.
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

// applyMCPAuthDeployMetadata fills the MCP-auth deploy fields from a desktop
// public key (PEM). It performs no side effects — the Secret is applied later
// at execution time by applyMCPAuthSecret. An empty key is a no-op, so a
// non-desktop deploy leaves the edge unauthenticated (back-compat). The issuer
// and audience derive from the shared conventions so the signer, the chart, and
// the verifier all agree.
func applyMCPAuthDeployMetadata(deployInput *HelmDeploySpec, publicKeyPEM string) {
	if deployInput == nil || strings.TrimSpace(publicKeyPEM) == "" {
		return
	}
	deployInput.MCPAuthEnabled = true
	deployInput.MCPAuthPublicKeyPEM = publicKeyPEM
	deployInput.MCPAuthIssuer = DesktopMCPIssuer()
	deployInput.MCPAuthAudience = MCPTokenAudience(deployInput.Tenant, deployInput.Environment)
	deployInput.MCPAuthSecretName = mcpAuthSecretName(deployInput.ReleaseName)
}
