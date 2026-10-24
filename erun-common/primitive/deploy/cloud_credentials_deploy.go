package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
)

func cloudflareSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-cloudflare"
}

// Only the token belongs in the Secret; %q renders valid YAML whatever the punctuation.
func renderCloudflareCredentialsSecret(name, namespace, apiToken string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-deploy
type: Opaque
stringData:
  CLOUDFLARE_API_TOKEN: %q
`, name, namespace, apiToken)
}

// applyCloudflareCredentialsSecret keeps the token out of argv, the trace, and helm values.
func applyCloudflareCredentialsSecret(ctx core.Context, deployInput seam.HelmDeploySpec) error {
	if !deployInput.CloudflareEnabled || strings.TrimSpace(deployInput.CloudflareSecretName) == "" {
		return nil
	}
	args := kube.KubectlApplyStdinArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply cloudflare credentials secret " + deployInput.CloudflareSecretName + " (token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if strings.TrimSpace(deployInput.CloudflareTokenRef) == "" {
		return fmt.Errorf("deploy %s: cloudflare alias has no token reference", deployInput.ReleaseName)
	}
	store, err := cloud.DefaultCloudSecretStore()
	if err != nil {
		return fmt.Errorf("resolve cloud secret store: %w", err)
	}
	token, err := store.LoadCloudSecret(deployInput.CloudflareTokenRef)
	if err != nil {
		return fmt.Errorf("load cloudflare api token: %w", err)
	}
	manifest := renderCloudflareCredentialsSecret(
		deployInput.CloudflareSecretName,
		deployInput.Namespace,
		token,
	)
	return kube.ApplySecretManifest(deployInput.KubernetesContext, deployInput.Namespace, "cloudflare credentials secret", manifest, args)
}

// applyCloudflareDeployMetadata fills Cloudflare deploy fields; the Secret is applied at execution.
func applyCloudflareDeployMetadata(store config.CloudReadStore, env config.EnvConfig, deployInput *seam.HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	alias := strings.TrimSpace(env.ResolvedCloudAliases()[config.CloudProviderCloudflare])
	if alias == "" {
		return
	}
	provider, err := cloud.ResolveCloudProvider(store, alias)
	if err != nil || provider.Provider != config.CloudProviderCloudflare || provider.Cloudflare == nil {
		return
	}
	deployInput.CloudflareEnabled = true
	deployInput.CloudflareAccountID = provider.Cloudflare.AccountID
	deployInput.CloudflareTokenRef = provider.Cloudflare.TokenRef
	deployInput.CloudflareSecretName = cloudflareSecretName(deployInput.ReleaseName)
}
