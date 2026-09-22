package eruncommon

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// platform_alias_secret.go provisions a *platform* credential into a freshly
// created environment's pod -- the counterpart of registry_credential_secret.go's
// registry credential, over the same channel and at the same point in init.
//
// The invariant: every platform command (`erun review show`, `erun exec
// gate-merge`, `erun review record-build --gate`, `erun exec gate-run
// start/report`, `erun review report-merged`, `erun gate list`) resolves through
// newPlatformClientForAlias, which needs a configured erun-type cloud provider
// alias; and the only way to create one the CLI offers requires an interactive
// OIDC login, which no unattended pod can complete. So the session has to be
// provisioned from outside, by the one process that is already signed in --
// `erun init` on the invoking host.
//
// The credential this delegates is that host's own operator identity, not a
// distinct machine identity: an environment's platform calls are attributed to
// whoever ran init. See erun-backend-api/AGENTS.md § "An agent environment
// cannot sign itself in to a platform alias" for what that costs and the
// machine-user design that would replace it.

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

// reconcilePlatformAliasSecret retrofits the platform alias onto an environment
// that was initialised before anything provisioned one for it.
//
// provisioning was reachable from init alone, so an environment initialised
// before it existed was left permanently unable to call the platform API:
// nothing mints the Secret, nothing records its name on the env, so the runtime
// chart mounts nothing and the entrypoint's seeder correctly does nothing. Deploy is where that is fixable, because it is the one step an
// existing environment takes that already has the invoking host's credentials in
// hand and already writes pre-rollout Secrets into the namespace -- the same
// place the registry credential, the MCP auth key and the image pull secrets are
// delivered from. Retrofitting here means an environment gains the alias on its
// next ordinary deploy, instead of needing to be destroyed and re-created, or an
// operator knowing to run a repair command before anything else can work.
//
// The gate is "this env records no alias name", which is exactly the
// pre-provisioning state and nothing else. An environment that names one is left
// alone even when this host is signed in: this Secret carries the *delegating
// operator's own identity*, so overwriting it on every deploy would silently
// rotate whichever person an environment is acting as, and a pod resolves its
// sole erun alias -- re-provisioning a different one is not a refresh, it is a
// change of identity. The remedy for a rotated or deleted Secret stays the one
// that already exists: re-run init.
//
// A host with no signed-in alias to give is a silent no-op rather than a traced
// one, the same rule the sibling pre-rollout helpers follow (applyMCPAuthSecret,
// refreshImagePullSecrets, recordMCPAuthKeyOnEnv): a deploy that has nothing to
// deliver for this leaves the release byte-for-byte as it was, and a decision
// that changes no input needs no line in a trace every runtime deploy carries.
func reconcilePlatformAliasSecret(ctx Context, deployInput *HelmDeploySpec) error {
	if deployInput == nil || deployInput.ReleaseName != RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	if strings.TrimSpace(deployInput.PlatformAliasSecretName) != "" {
		return nil
	}
	store, deps := ConfigStore{}, DefaultCloudDependencies()
	// Resolved before provisioning so "this host has nothing to give" is
	// distinguishable from "there was nothing to do" without either case
	// leaving a trace. The provisioning below re-resolves the same local,
	// read-only state through the one implementation that already owns it.
	if _, _, ok := resolveHostPlatformAlias(store, deps); !ok {
		return nil
	}
	name, err := provisionPlatformAliasSecret(ctx, store, deployInput.Tenant, deployInput.Namespace, deployInput.KubernetesContext, deps)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	// The chart renders the volume and its mount only when the deploy names the
	// Secret, so the name has to reach the helm upgrade this deploy is about to
	// run -- otherwise the Secret would be created for a release that keeps
	// mounting nothing.
	deployInput.PlatformAliasSecretName = name
	return recordPlatformAliasSecretNameOnEnv(ctx, deployInput.Tenant, deployInput.Environment, name)
}

// recordPlatformAliasSecretNameOnEnv records the Secret's name on the
// environment, at the moment the deploy hands it to the cluster rather than once
// the rollout has succeeded, for the reason recordMCPAuthKeyOnEnv does: the
// chart is about to mount it, so the environment must name it before the pod
// that reads it exists. Without the record the next deploy would read an empty
// field, resolve the host again, and -- worse -- a deploy whose host had since
// lost its alias would pass no name at all, dropping the mount from an
// environment that had one.
func recordPlatformAliasSecretNameOnEnv(ctx Context, tenant, environment, name string) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return nil
	}
	// The read runs in both modes so a dry run traces the write only where a
	// real one would perform it: an env already naming this Secret needs
	// nothing. It cannot normally be reached with the record already in place
	// (the caller gates on the name being empty), so this is the reconciliation
	// against the store rather than against the plan.
	envConfig, _, err := LoadEnvConfig(tenant, environment)
	if err == nil && strings.TrimSpace(envConfig.PlatformAliasSecretName) == name {
		return nil
	}
	ctx.Trace("deploy: platform alias: recording secret " + name + " on " + tenant + "/" + environment)
	if ctx.DryRun {
		return nil
	}
	if err != nil {
		ctx.Trace("deploy: platform alias: could not read the env config of " + tenant + "/" + environment + " to record " + name)
		return nil
	}
	envConfig.PlatformAliasSecretName = name
	if err := SaveEnvConfig(tenant, envConfig); err != nil {
		return fmt.Errorf("record platform alias secret on %s/%s: %w", tenant, environment, err)
	}
	return nil
}
