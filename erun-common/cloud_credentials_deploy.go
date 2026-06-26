package eruncommon

import (
	"fmt"
	"strings"
)

// cloudflareSecretName derives the per-release Secret that carries the
// Cloudflare credentials for an environment's runtime pod. It mirrors the
// erun-docs cf-creds shape (CLOUDFLARE_API_TOKEN + CLOUDFLARE_ACCOUNT_ID).
func cloudflareSecretName(releaseName string) string {
	return strings.TrimSpace(releaseName) + "-cloudflare"
}

// cloudflareSecretApplyArgs builds the kubectl argv that applies the Cloudflare
// credentials Secret, reading the manifest from stdin (`-f -`) so the token is
// never an argv element. Shared by the dry-run trace and the live apply.
func cloudflareSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// renderCloudflareCredentialsSecret renders the Opaque Secret manifest carrying
// only the sensitive token; the non-secret account id rides as a plain helm
// value. The token is %q-quoted (valid YAML, since YAML is a JSON superset) so
// any punctuation is safely escaped.
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

// applyCloudflareCredentialsSecret creates or updates the Secret that delivers
// the delegated Cloudflare token into the env's runtime pod. The token is
// loaded from the default secret store and piped to `kubectl apply -f -` via
// stdin, so it never appears in argv, the trace, or helm release values. It is
// a no-op unless the env attached a Cloudflare alias; dry-run traces the apply
// (token redacted) without touching the cluster.
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

// applyCloudflareDeployMetadata fills the Cloudflare deploy fields from the
// env's attached Cloudflare alias (if any). It performs no side effects — the
// Secret is applied later at execution time by applyCloudflareCredentialsSecret.
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
