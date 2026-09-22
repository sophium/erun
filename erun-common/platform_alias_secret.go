package eruncommon

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// platform_alias_secret.go provisions a *platform* credential into a freshly
// created environment's pod, the counterpart of registry_credential_secret.go's
// registry credential: `erun init` runs on a host that is already signed in to
// erun's hosted platform, and mints a Secret carrying that alias and its stored
// refresh token, so the pod it deploys can read and write the platform API from
// its first boot.
//
// The gap this closes: every platform command (`erun review show`,
// `erun exec gate-merge`, `erun review record-build --gate`, `erun exec
// gate-run start/report`, `erun review report-merged`, `erun gate list`)
// resolves through newPlatformClientForAlias, which needs a configured
// erun-type cloud provider alias. The only way to create one used to be
// `erun cloud init erun --api-url <url>` followed by `erun cloud login erun`,
// and login offers only OIDC device-authorization or authorization-code+PKCE --
// both needing a human at a browser, and neither completable from an unattended
// pod. So an environment promoted to drive the merge queue could not make a
// single call, and had no way to acquire the ability. Hand-carrying the alias
// between pods was the workaround; this makes provisioning do it.

// platformAliasSecretName derives the per-environment Secret `erun init` mints
// from the operator's own signed-in erun platform alias.
func platformAliasSecretName(tenant string) string {
	return RuntimeReleaseName(tenant) + "-platform-alias"
}

// The runtime chart mounts this Secret read-only at
// "/etc/erun/platform-alias", where the entrypoint's sync_platform_alias reads
// it. That path belongs to those two, not here: init only mints the Secret, and
// a Go copy of the path would be a third name for it that nothing enforces.
//
// The Secret's three keys. The entrypoint combines them into the two files the
// pod's own `erun` actually reads: the cloudproviders entry is appended to
// ~/.config/erun/config.yaml, and the token is written to the path the file
// secret store resolves that alias's refresh-token ref to.
const (
	// platformAliasEntryKey holds one `cloudproviders:` list *item* -- the alias
	// entry, and nothing else. Deliberately not the whole `cloudproviders:` key:
	// the pod's root config already emits that key for the infrastructure
	// provider the chart injected, and a second one is a duplicate mapping key,
	// which the config reader refuses outright rather than merging. The item is
	// rendered here, where the values are typed, and the entrypoint only has to
	// place it inside the block that already exists.
	platformAliasEntryKey = "cloud-provider-entry.yaml"
	// platformAliasSecretFileKey holds the *basename* the token must be saved
	// as. The store names a secret file after a hash of its ref, which the pod's
	// entrypoint deliberately does not reimplement in shell -- init computes it
	// with the store's own function so the two cannot drift.
	platformAliasSecretFileKey = "cloud-secret-file"
	// platformAliasSecretTokenKey holds the refresh token itself.
	platformAliasSecretTokenKey = "cloud-secret-token"
)

// renderPlatformAliasEntry renders the provisioned alias as one list item of the
// pod's `cloudproviders:` sequence, indented to sit under that key.
//
// It marshals a bare sequence and re-indents it rather than marshalling the
// mapping, because yaml.v3's default indent (4) is not the indent the runtime
// entrypoint writes that block with (2), and YAML requires every item of a block
// sequence to share one indentation -- mixing the two is a parse error, not a
// cosmetic difference.
func renderPlatformAliasEntry(provider CloudProviderConfig) (string, error) {
	// The refresh-token reference travels with the alias, exactly as it does in
	// an operator's own config.yaml. The token itself never appears here -- it
	// goes to the secret store, under a ref the alias points at.
	data, err := yaml.Marshal([]CloudProviderConfig{provider})
	if err != nil {
		return "", fmt.Errorf("render platform alias entry: %w", err)
	}
	// Every line shifts by the same two columns: yaml already aligns the item's
	// own mapping keys under the first key regardless of the item's own indent,
	// so a uniform shift preserves that alignment while moving "- " from column
	// 0 (top-level sequence) to column 2 (a sequence under a mapping key).
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// resolveHostPlatformAlias resolves the invoking host's sole erun platform
// alias together with the refresh token the host is signed in with. ok is false
// whenever this host has nothing to give -- no erun alias, several (which alias
// to give the pod is then the operator's call, not a guess), one with no
// stored session, or no readable secret store -- and every such case is a
// no-op rather than an error, matching provisionRegistryCredentialSecret.
func resolveHostPlatformAlias(store CloudReadStore, deps CloudDependencies) (CloudProviderConfig, string, bool) {
	if store == nil || deps.CloudSecretStore == nil {
		return CloudProviderConfig{}, "", false
	}
	// The empty selection means "the operator's sole erun alias"; an ambiguous
	// or absent one is an error here, and both resolve to "nothing to give".
	provider, err := ResolveERunPlatformAlias(store, "")
	if err != nil {
		return CloudProviderConfig{}, "", false
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.RefreshTokenRef) == "" {
		return CloudProviderConfig{}, "", false
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.ERun.RefreshTokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return CloudProviderConfig{}, "", false
	}
	return provider, token, true
}

// renderPlatformAliasSecret wraps the entry and token in an Opaque Secret.
// `kubectl apply` upserts by name, so re-running init refreshes a stale token
// rather than failing on an existing Secret.
func renderPlatformAliasSecret(name, namespace, entry, secretFile, token string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-init
type: Opaque
stringData:
  %s: %q
  %s: %q
  %s: %q
`, name, namespace,
		platformAliasEntryKey, entry,
		platformAliasSecretFileKey, secretFile,
		platformAliasSecretTokenKey, token)
}

// provisionPlatformAliasSecret resolves the invoking host's signed-in erun
// platform alias and, when the host has one to give, mints or refreshes the
// Secret the runtime chart mounts so the pod can call the platform API. It
// returns the secret name to record on the environment, or "" when the host had
// nothing to provision, in which case the pod's own alias state is left exactly
// as it was.
//
// Host alias resolution is a local, read-only lookup (the caller's config store
// plus the secret store), so it still runs under dry-run: the decision it makes
// belongs in the trace either way.
func provisionPlatformAliasSecret(ctx Context, store CloudReadStore, tenant, namespace, kubernetesContext string, deps CloudDependencies) (string, error) {
	provider, token, ok := resolveHostPlatformAlias(store, deps)
	if !ok {
		ctx.Trace("platform alias: no signed-in erun cloud provider alias on this host; leaving the pod's own alias state untouched")
		return "", nil
	}
	entry, err := renderPlatformAliasEntry(provider)
	if err != nil {
		return "", err
	}
	name := platformAliasSecretName(tenant)
	args := kubectlApplyStdinArgs(namespace, kubernetesContext)
	ctx.Trace("apply platform alias secret " + name + " (alias " + provider.Alias + ", token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return name, nil
	}
	manifest := renderPlatformAliasSecret(name, namespace, entry, cloudSecretFileName(provider.ERun.RefreshTokenRef), token)
	if err := applySecretManifest(kubernetesContext, namespace, "platform alias secret", manifest, args); err != nil {
		return "", err
	}
	return name, nil
}
