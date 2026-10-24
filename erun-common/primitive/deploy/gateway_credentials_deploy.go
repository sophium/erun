package deploy

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// renderGatewayCredentialsSecret renders the namespace Secret holding the erun-level gateway credential.
func renderGatewayCredentialsSecret(name, namespace, authToken string) string {
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
`, name, namespace, config.GatewaySecretKey, authToken)
}

// applyGatewayCredentialsSecret delivers the gateway credential, keeping it out of argv,
// traces, and helm values. A missing credential errors rather than leave the chart
// pointing at a missing Secret.
func applyGatewayCredentialsSecret(ctx core.Context, deployInput HelmDeploySpec) error {
	// Resolved like the chart values, so the Secret exists exactly when the env reads it.
	gateway := config.EffectiveGateway(deployInput.Claude, deployInput.OpenRouter)
	if !gateway.Configured() {
		return nil
	}
	args := kube.KubectlApplyStdinArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply gateway credentials secret " + config.GatewaySecretName + " (token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	authToken, err := gatewayCredentialForDeploy(gateway)
	if err != nil {
		return err
	}
	manifest := renderGatewayCredentialsSecret(config.GatewaySecretName, deployInput.Namespace, authToken)
	return kube.ApplySecretManifest(deployInput.KubernetesContext, deployInput.Namespace, "gateway credentials secret", manifest, args)
}

// pruneGatewayCredentialsSecret removes the credential after a runtime rollout
// without the gateway; it runs after rollout because the old pod still references it.
func pruneGatewayCredentialsSecret(ctx core.Context, deployInput HelmDeploySpec) error {
	if deployInput.ReleaseName != config.RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	if config.EffectiveGateway(deployInput.Claude, deployInput.OpenRouter).Configured() {
		return nil
	}
	args := []string{}
	if c := strings.TrimSpace(deployInput.KubernetesContext); c != "" {
		args = append(args, "--context", c)
	}
	args = append(args, "-n", strings.TrimSpace(deployInput.Namespace), "delete", "secret", config.GatewaySecretName, "--ignore-not-found")
	ctx.Trace("remove gateway credentials secret " + config.GatewaySecretName + " (environment does not use the gateway)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	return deleteGatewayCredentialsSecret(args)
}

var deleteGatewayCredentialsSecret = func(args []string) error {
	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl delete gateway credentials secret: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

// gatewayCredentialForDeploy prefers the saved credential, falling back to this
// machine's Claude Code key for the same gateway.
func gatewayCredentialForDeploy(gateway *config.OpenRouterConfig) (string, error) {
	store, err := cloud.DefaultCloudSecretStore()
	if err != nil {
		return "", fmt.Errorf("resolve cloud secret store: %w", err)
	}
	return ResolveGatewayCredentialFromStore(store, gateway.AuthTokenRefName(), gateway.Endpoint())
}

// ResolveGatewayCredential resolves a host-side session's credential with deploy's
// precedence. The result is secret: deliver it only through a process environment.
func ResolveGatewayCredential(gateway *config.OpenRouterConfig) (string, error) {
	return gatewayCredentialForDeploy(gateway)
}

// ResolveGatewayCredentialFromStore reuses the host key only when the host already
// points at baseURL, since a credential is issued for one service.
func ResolveGatewayCredentialFromStore(store seam.CloudSecretStore, ref, baseURL string) (string, error) {
	saved, loadErr := store.LoadCloudSecret(ref)
	switch {
	case loadErr == nil && strings.TrimSpace(saved) != "":
		return strings.TrimSpace(saved), nil
	case loadErr != nil && !errors.Is(loadErr, os.ErrNotExist):
		// An unreadable store might hold a value that should win, so stop rather than
		// send a different credential.
		return "", fmt.Errorf("load gateway credential %q: %w", ref, loadErr)
	}
	hostToken, hostEndpoint := HostClaudeGatewayCredential()
	if hostToken != "" && SameGatewayEndpoint(hostEndpoint, baseURL) {
		return hostToken, nil
	}
	return "", noGatewayCredentialError(ref, baseURL, hostToken, hostEndpoint)
}

// noGatewayCredentialError names whatever the host does hold, so the operator is
// not told no credential exists when one plainly does.
func noGatewayCredentialError(ref, baseURL, hostToken, hostEndpoint string) error {
	switch {
	case hostToken != "" && hostEndpoint != "":
		return fmt.Errorf(
			"no gateway credential: nothing is saved under %q, and this machine's Claude Code key belongs to %s — not to %s; save this gateway's own key",
			ref, hostEndpoint, strings.TrimSpace(baseURL),
		)
	case hostToken != "":
		return fmt.Errorf(
			"no gateway credential: nothing is saved under %q, and this machine's Claude Code authenticates directly rather than through a gateway, so its key is not sent to one; save this gateway's own key",
			ref,
		)
	default:
		return fmt.Errorf(
			"no gateway credential: nothing is saved under %q and this machine's Claude Code settings carry none",
			ref,
		)
	}
}
