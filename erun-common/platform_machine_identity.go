package eruncommon

import (
	"context"
	"fmt"
	"strings"
)

// platform_machine_identity.go delivers a *machine* identity into an
// environment's pod, over the same channel and at the same point as
// platform_alias_secret.go delivers a delegated one.
//
// The two are the same Secret with the same three keys, mounted at the same
// path and seeded by the same entrypoint function, and that is deliberate: an
// environment has one platform identity, not two. Which kind it is, is a
// property of the entry inside -- a refresh-token reference means a signed-in
// person's session was delegated to it, a client-secret reference means it
// authenticates as itself. Nothing downstream has to know the difference: the
// entrypoint writes whichever credential the entry's reference names, and the
// pod's own token resolution picks the grant that reference implies.
//
// This is also the constraint a migration has to work within: EnvConfig carries
// one secret name, so an environment is on one identity or the other and never
// both, and switching it is a re-provision rather than a merge.

// machineIdentityClientSecretRef is the secret-store reference a machine
// identity's client secret is saved under, distinct from the refresh-token
// reference so an alias can never resolve the wrong credential's file.
func machineIdentityClientSecretRef(alias string) string {
	return "erun/clientsecret/" + strings.TrimSpace(alias)
}

// provisionedMachineIdentity is a machine identity resolved all the way to
// what gets written: the alias entry the pod reads, and the credential it
// points at.
type provisionedMachineIdentity struct {
	provider     CloudProviderConfig
	clientSecret string
	// subject names the identity in the trace, so an operator reading a deploy
	// can tell which identity an environment was given without reading the
	// Secret.
	subject string
}

// provisionMachineIdentitySecret asks the platform for environment's own
// machine identity and, when it gets a usable one, mints the Secret the
// runtime chart already mounts.
//
// ok is false whenever there is nothing to deliver -- this host has no
// signed-in erun alias to ask with, the environment is not registered on the
// platform yet, the platform will not mint an identity for this tenant, or the
// credential it returned could not be exchanged for a token. Every one of
// those is a fall-back to the delegating alias rather than an error, because
// that is what the environment has today and losing it would be a regression
// dressed up as a feature; each is traced, because a deployment that silently
// stayed on the operator's identity is indistinguishable from one that never
// tried.
func provisionMachineIdentitySecret(ctx Context, store CloudReadStore, tenant, environment, namespace, kubernetesContext string, deps CloudDependencies) (string, bool, error) {
	provisioned, ok, err := resolveMachineIdentity(ctx, store, environment, deps)
	if err != nil || !ok {
		return "", false, err
	}
	entry, err := renderPlatformAliasEntry(provisioned.provider)
	if err != nil {
		return "", false, err
	}
	ref := machineIdentityClientSecretRef(provisioned.provider.Alias)
	name := platformAliasSecretName(tenant)
	args := kubectlApplyStdinArgs(namespace, kubernetesContext)
	ctx.Trace("apply machine identity secret " + name + " (identity " + provisioned.subject + ", client secret redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	manifest := renderPlatformAliasSecret(name, namespace, entry, cloudSecretFileName(ref), provisioned.clientSecret)
	if err := applySecretManifest(kubernetesContext, namespace, "machine identity secret", manifest, args); err != nil {
		return "", false, err
	}
	ctx.Trace("machine identity: " + environment + " now authenticates as its own identity (" + provisioned.subject + ")")
	return name, true, nil
}

// resolveMachineIdentity walks every step from "this host" to "a credential
// proven to mint a token", stopping at the first one it cannot complete.
//
// The credential is exchanged for a token *before* anything is written. A
// machine identity that cannot mint a token is worse than none at all -- it
// replaces a working delegated session with a pod that can reach nothing --
// and the only moment erun can tell the two apart is here, in the process that
// just minted it.
func resolveMachineIdentity(ctx Context, store CloudReadStore, environment string, deps CloudDependencies) (provisionedMachineIdentity, bool, error) {
	deps = normalizeCloudDependencies(deps)
	client, host, ok, err := machineIdentityPlatformClient(ctx, store, deps)
	if err != nil || !ok {
		return provisionedMachineIdentity{}, false, err
	}
	if ctx.DryRun {
		// The environment's platform id and the identity itself are both
		// server-side facts, so a preview can only name what it would ask for.
		ctx.Trace("machine identity: would provision " + environment + "'s own platform identity through " + host.ERun.APIURL)
		return provisionedMachineIdentity{}, false, nil
	}
	identity, ok := fetchMachineIdentity(ctx, client, environment)
	if !ok {
		return provisionedMachineIdentity{}, false, nil
	}
	if err := verifyMachineIdentityCredential(ctx, identity, deps); err != nil {
		ctx.Trace("machine identity: the credential the platform minted for " + environment + " could not be exchanged for a token (" + err.Error() + "); leaving its platform identity as it is")
		return provisionedMachineIdentity{}, false, nil
	}
	return provisionedMachineIdentity{
		provider:     machineIdentityProviderConfig(host, identity),
		clientSecret: identity.ClientSecret,
		subject:      identity.Subject,
	}, true, nil
}

// machineIdentityPlatformClient is the platform client provisioning calls with:
// this host's own signed-in alias, since that is the only credential the
// invoking process has. Nothing about it reaches the environment.
func machineIdentityPlatformClient(ctx Context, store CloudReadStore, deps CloudDependencies) (*PlatformClient, CloudProviderConfig, bool, error) {
	// Re-resolved rather than threaded from the caller so "this host has
	// nothing to ask with" is decided in the one place that already decides it,
	// and without a second network-free resolution to keep in step.
	provider, _, ok, err := resolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if err != nil {
		return nil, CloudProviderConfig{}, false, err
	}
	if !ok {
		// A host with no alias to ask with is a silent no-op rather than a
		// traced one, the same rule the sibling delegated path follows: the
		// fall-back it triggers leaves the environment byte-for-byte as it was
		// and says so itself, so a second line here would only double a
		// statement every runtime deploy already carries.
		return nil, CloudProviderConfig{}, false, nil
	}
	client, provider, err := newPlatformClientForAlias(ctx, store, provider.Alias, deps)
	if err != nil {
		return nil, CloudProviderConfig{}, false, err
	}
	return client, provider, true, nil
}

// fetchMachineIdentity resolves the environment's platform id and asks for its
// identity, reporting the reason it could not when it could not.
func fetchMachineIdentity(ctx Context, client *PlatformClient, environment string) (PlatformMachineIdentity, bool) {
	environmentID, err := resolvePlatformEnvironmentID(context.Background(), client, environment)
	if err != nil {
		ctx.Trace("machine identity: " + err.Error() + "; leaving the environment's platform identity as it is")
		return PlatformMachineIdentity{}, false
	}
	identity, err := client.MintMachineIdentity(context.Background(), environmentID)
	if err != nil {
		ctx.Trace("machine identity: the platform would not mint one for " + environment + " (" + err.Error() + "); leaving its platform identity as it is")
		return PlatformMachineIdentity{}, false
	}
	if strings.TrimSpace(identity.ClientID) == "" || strings.TrimSpace(identity.ClientSecret) == "" {
		ctx.Trace("machine identity: the platform returned no usable credential for " + environment + "; leaving its platform identity as it is")
		return PlatformMachineIdentity{}, false
	}
	return identity, true
}

// machineIdentityProviderConfig is the host's erun alias with its credential
// swapped for the machine identity's: same platform, same alias name (so
// `ResolveERunPlatformAlias` finds it and the pod still holds exactly one erun
// alias), same API URL -- a different way of proving who is calling.
//
// The issuer is the identity's own rather than the host's, because that is
// where its tokens are minted; where a tenant's identity lives in the
// platform's issuer the two are already the same value.
func machineIdentityProviderConfig(host CloudProviderConfig, identity PlatformMachineIdentity) CloudProviderConfig {
	provider := host
	provider.OIDCIssuerURL = strings.TrimSpace(identity.Issuer)
	provider.ERun = &ERunProviderConfig{
		APIURL:          host.ERun.APIURL,
		ClientID:        strings.TrimSpace(identity.ClientID),
		ClientSecretRef: machineIdentityClientSecretRef(host.Alias),
	}
	return NormalizeCloudProviderConfig(provider)
}

// verifyMachineIdentityCredential exchanges the freshly minted credential for
// a token, the one check that stands between a working environment and one
// whose platform access has just been replaced by a credential nothing
// accepts.
func verifyMachineIdentityCredential(ctx Context, identity PlatformMachineIdentity, deps CloudDependencies) error {
	discovery, err := deps.FetchOIDCDiscovery(ctx, strings.TrimSpace(identity.Issuer))
	if err != nil {
		return err
	}
	tokens, err := deps.ClientCredentialsERunTokens(ctx, discovery, identity.ClientID, identity.ClientSecret)
	if err != nil {
		return err
	}
	if strings.TrimSpace(tokens.AccessToken) == "" {
		return fmt.Errorf("the token endpoint returned no access token")
	}
	return nil
}
