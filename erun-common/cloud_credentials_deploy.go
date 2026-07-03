package eruncommon

import (
	"fmt"
	"strings"
)

func cloudflareSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-cloudflare"
}

// Applying via -f - keeps the credential manifest on stdin, so the token never
// lands in argv or the trace.
func cloudflareSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// Only the token belongs in the Secret; the account id rides as a plain helm
// value. %q renders it as valid YAML (a JSON superset) so punctuation can't
// break the manifest.
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

// applyCloudflareCredentialsSecret delivers the delegated Cloudflare token into
// the env's runtime pod, keeping it out of argv, the trace, and helm release
// values.
func applyCloudflareCredentialsSecret(ctx Context, deployInput HelmDeploySpec) error {
	if !deployInput.CloudflareEnabled || strings.TrimSpace(deployInput.CloudflareSecretName) == "" {
		return nil
	}
	args := cloudflareSecretApplyArgs(deployInput.Namespace, deployInput.KubernetesContext)
	ctx.Trace("apply cloudflare credentials secret " + deployInput.CloudflareSecretName + " (token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if strings.TrimSpace(deployInput.CloudflareTokenRef) == "" {
		return fmt.Errorf("deploy %s: cloudflare alias has no token reference", deployInput.ReleaseName)
	}
	store, err := DefaultCloudSecretStore()
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
	cmd := Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply cloudflare credentials secret: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applyCloudflareDeployMetadata populates the Cloudflare deploy fields from the
// env's attached alias; the Secret itself is applied later at execution time,
// not here.
func applyCloudflareDeployMetadata(store CloudReadStore, env EnvConfig, deployInput *HelmDeploySpec) {
	if deployInput == nil {
		return
	}
	alias := strings.TrimSpace(env.ResolvedCloudAliases()[CloudProviderCloudflare])
	if alias == "" {
		return
	}
	provider, err := ResolveCloudProvider(store, alias)
	if err != nil || provider.Provider != CloudProviderCloudflare || provider.Cloudflare == nil {
		return
	}
	deployInput.CloudflareEnabled = true
	deployInput.CloudflareAccountID = provider.Cloudflare.AccountID
	deployInput.CloudflareTokenRef = provider.Cloudflare.TokenRef
	deployInput.CloudflareSecretName = cloudflareSecretName(deployInput.ReleaseName)
}
