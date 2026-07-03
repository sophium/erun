package eruncommon

import (
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
// backend component charts never carry mcpAuth. A blank key path is a no-op,
// leaving a non-desktop deploy unauthenticated (back-compat).
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
// from the shared conventions so signer, chart, and verifier all agree.
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
