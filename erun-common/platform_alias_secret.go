package eruncommon

import (
	"errors"
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

// resolveHostPlatformAlias resolves which erun platform alias this run should
// delegate to the pod, together with the refresh token the invoking host is
// signed in with.
//
// alias is the operator's own explicit selection, empty when they made none.
// Given one, it is resolved directly -- the same resolution `erun
// platform --erun-alias` and the desktop's own alias override use -- which is
// what lets a host carrying several configured erun aliases say which one an
// environment should act as. Given none, the host's *sole* erun alias is
// resolved: an ambiguous or absent one is then the operator's call rather than
// a guess, so it stays "nothing to give".
//
// ok is false whenever this host has nothing to give -- no erun alias, several,
// one with no stored session, or no readable secret store -- and each of those
// is a no-op rather than a failure, matching provisionRegistryCredentialSecret.
//
// err is non-nil with ok false in exactly two cases, and the caller's own
// selection is what tells them apart, which is why both are returned rather
// than one being dropped here. An operator who named an alias gets a failure
// carrying the selection, the offending alias, and the cause: declining
// silently would leave them with a deploy that reported success and an
// environment that still cannot call the platform, with nothing saying why. An
// operator who named none gets the several-aliases ambiguity and nothing else:
// it is a decline rather than a failure, and it is reported because it is the
// one silent-looking no-op the operator can actually lift. Every other unnamed
// decline -- no alias, no session, no readable store -- is nil, so a caller
// that names no alias keeps the silence it has always had.
func resolveHostPlatformAlias(store CloudReadStore, deps CloudDependencies, alias string) (CloudProviderConfig, string, bool, error) {
	explicit := hostAliasSelected(alias)
	if store == nil || deps.CloudSecretStore == nil {
		return CloudProviderConfig{}, "", false, hostAliasUnusable(
			explicit, alias, errors.New("this host has no readable cloud secret store, so the alias's signed-in session cannot be delivered to the environment"))
	}
	provider, err := ResolveERunPlatformAlias(store, alias)
	if err != nil {
		return CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, err)
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.RefreshTokenRef) == "" {
		return CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, noStoredSessionError(provider.Alias))
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.ERun.RefreshTokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, noStoredSessionError(provider.Alias))
	}
	return provider, token, true, nil
}

// hostAliasSelected reports whether the operator named the alias themselves.
// Both answers a resolve can give turn on it -- a named alias that cannot be
// honoured is a failure, an unnamed one that cannot be is a no-op -- so the
// callers that have to tell those apart read it here instead of repeating the
// test and drifting from it.
func hostAliasSelected(alias string) bool {
	return strings.TrimSpace(alias) != ""
}

// hostAliasUnusable is the pair of answers an alias this host cannot deliver
// has. An operator who named it gets a failure carrying the selection, the
// offending alias, and the cause; one who named none gets the silent "this host
// has nothing to give" that every caller of resolveHostPlatformAlias has always
// treated as a no-op, since nothing they said is being ignored.
//
// The named-none half has one exception, and it is the reason this takes the
// cause rather than only the explicitness. A host with several erun aliases and
// no selection is not a host with nothing to give -- it has two things to give
// and no unambiguous answer, which is a decline the operator can lift -- so
// reporting it as the same silence an absent alias gets leaves the retrofit
// declining on precisely the hosts it was built for, with no record anywhere.
// The ambiguity is therefore returned rather than dropped, and a caller decides
// whether to report it; an absent alias, an absent session and an unreadable
// store are all genuinely nothing to say and stay nil.
func hostAliasUnusable(explicit bool, alias string, cause error) error {
	if explicit {
		return fmt.Errorf("--erun-alias %q: %w", alias, cause)
	}
	if isPlatformAliasAmbiguity(cause) {
		return cause
	}
	return nil
}

// platformAliasDeclineLine renders the one line a caller that provisioned
// nothing owes its operator, in either of the two shapes that decline has.
//
// The nil shape is the line init has always emitted and a routine deploy has
// always suppressed -- a host with no alias to give changes no input, and a
// decision that changes no input needs no line in a trace every runtime deploy
// carries -- so a caller that must stay silent does so by not calling this at
// all, never by asking it to render nothing.
//
// The ambiguity shape is the exception, and the reason this exists: the
// operator has a real choice to make, the environment is left unable to call
// the platform until they make it, and the only other place that says so is a
// pod-side refusal naming a different cause and a remedy unreachable from
// inside a pod. Naming the cause and the remedy here is what connects the two.
func platformAliasDeclineLine(cause error) string {
	if cause == nil {
		return "platform alias: no signed-in erun cloud provider alias on this host; leaving the pod's own alias state untouched"
	}
	return "platform alias: " + cause.Error() +
		"; no alias was provisioned for this environment, so its pod cannot call the platform until a deploy names one"
}

// noStoredSessionError names the alias and the command that would sign this host
// in to it, since "no session" on its own leaves the operator to work out which
// of their configured aliases is missing one and how to give it one.
func noStoredSessionError(alias string) error {
	return fmt.Errorf("this host holds no session for it; run `erun cloud login %s` first", alias)
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
	provider, token, ok, cause := resolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if !ok {
		if hostAliasSelected(ctx.PlatformAlias) {
			// The one decline that is a failure: the operator named the alias, so
			// ignoring it would leave them with an environment that cannot call
			// the platform and nothing saying why.
			return "", cause
		}
		ctx.Trace(platformAliasDeclineLine(cause))
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
// A host carrying several configured erun aliases has no unambiguous answer to
// "whose identity", so the sole-alias default declines there rather than picking
// one. Context.PlatformAlias -- `erun deploy --erun-alias` -- is how the
// operator supplies that answer: the named alias is then resolved directly, and
// one that cannot be honoured is reported rather than declined, since the
// operator asked for it by name. It selects which identity a *due* provisioning
// delegates, so an environment already recording one is still left alone.
//
// A host with no signed-in alias to give is a silent no-op rather than a traced
// one, the same rule the sibling pre-rollout helpers follow (applyMCPAuthSecret,
// refreshImagePullSecrets, recordMCPAuthKeyOnEnv): a deploy that has nothing to
// deliver for this leaves the release byte-for-byte as it was, and a decision
// that changes no input needs no line in a trace every runtime deploy carries.
//
// The several-alias host above is not that host, and the difference is the one
// this function has to keep: it has something to give and no unambiguous answer,
// which is a decline the operator lifts by naming one, not an absence. Left
// silent it is indistinguishable from the no-op, so the deploy provisions
// nothing on exactly the hosts that triggered the retrofit and records nothing
// anywhere -- the pod's own refusal names a different cause and a remedy that
// cannot work from inside a pod. So the decline is traced, and only it: the
// absence above still adds nothing.
func reconcilePlatformAliasSecret(ctx Context, deployInput *HelmDeploySpec) error {
	if deployInput == nil || deployInput.ReleaseName != RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	if strings.TrimSpace(deployInput.PlatformAliasSecretName) != "" {
		// Only the operator's own selection earns a line here. They named an
		// alias, this deploy will not act on it, and the reason is a decision
		// they already made once -- saying so is the difference between a flag
		// that was deliberately not applied and one that silently did nothing.
		if strings.TrimSpace(ctx.PlatformAlias) != "" {
			ctx.Trace("platform alias: this environment already records " + deployInput.PlatformAliasSecretName +
				", so --erun-alias " + ctx.PlatformAlias + " is unused; re-run `erun init` to re-provision it")
		}
		return nil
	}
	store, deps := ConfigStore{}, DefaultCloudDependencies()
	// Resolved before provisioning so "this host has nothing to give" is
	// distinguishable from "there was nothing to do", and so the reason is in
	// hand before the provisioning below re-resolves the same local, read-only
	// state through the one implementation that already owns it. An alias the
	// operator named explicitly is reported rather than swallowed, because a
	// decline they did not ask for is indistinguishable from the retrofit never
	// having run. The ambiguity is the one decline they named nothing for that
	// is still theirs to lift, so it gets the one line a deploy may add here;
	// every other unnamed decline adds nothing, which is the rule this gate
	// exists to keep.
	_, _, ok, cause := resolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if !ok {
		if hostAliasSelected(ctx.PlatformAlias) {
			return cause
		}
		if cause != nil {
			ctx.Trace(platformAliasDeclineLine(cause))
		}
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
