package setup

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// A machine identity uses the same Secret, keys and mount as the delegated alias
// (platform_alias_secret.go); the entry's secret reference decides which grant
// the pod uses. EnvConfig holds one secret name, so an env has one or the other.

// machineIdentityClientSecretRef differs from the refresh-token reference so an
// alias never resolves the wrong credential file.
func machineIdentityClientSecretRef(alias string) string {
	return "erun/clientsecret/" + strings.TrimSpace(alias)
}

type provisionedMachineIdentity struct {
	provider     model.CloudProviderConfig
	clientSecret string
	subject      string
}

// provisionMachineIdentitySecret mints the env's machine-identity Secret. ok is
// false, with a trace, whenever there is nothing usable to deliver; the caller
// then falls back to the delegated alias.
func provisionMachineIdentitySecret(ctx core.Context, store config.CloudReadStore, tenant, environment, namespace, kubernetesContext string, deps cloud.CloudDependencies) (string, bool, error) {
	provisioned, ok, err := resolveMachineIdentity(ctx, store, environment, deps)
	if err != nil || !ok {
		return "", false, err
	}
	entry, err := deploy.RenderPlatformAliasEntry(provisioned.provider)
	if err != nil {
		return "", false, err
	}
	ref := machineIdentityClientSecretRef(provisioned.provider.Alias)
	name := deploy.PlatformAliasSecretName(tenant)
	args := kube.KubectlApplyStdinArgs(namespace, kubernetesContext)
	ctx.Trace("apply machine identity secret " + name + " (identity " + provisioned.subject + ", client secret redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	manifest := deploy.RenderPlatformAliasSecret(name, namespace, entry, cloud.CloudSecretFileName(ref), provisioned.clientSecret)
	if err := kube.ApplySecretManifest(kubernetesContext, namespace, "machine identity secret", manifest, args); err != nil {
		return "", false, err
	}
	ctx.Trace("machine identity: " + environment + " now authenticates as its own identity (" + provisioned.subject + ")")
	return name, true, nil
}

// resolveMachineIdentity proves the credential mints a token before anything is
// written: an unusable identity is worse than the delegated session it replaces.
func resolveMachineIdentity(ctx core.Context, store config.CloudReadStore, environment string, deps cloud.CloudDependencies) (provisionedMachineIdentity, bool, error) {
	deps = cloud.NormalizeCloudDependencies(deps)
	client, host, ok, err := machineIdentityPlatformClient(ctx, store, deps)
	if err != nil || !ok {
		return provisionedMachineIdentity{}, false, err
	}
	if ctx.DryRun {
		// Both are server-side facts, so a preview can only name the request.
		ctx.Trace("machine identity: would provision " + environment + "'s own platform identity through " + host.ERun.APIURL)
		return provisionedMachineIdentity{}, false, nil
	}
	identity, ok := fetchMachineIdentity(ctx, client, environment)
	if !ok {
		return provisionedMachineIdentity{}, false, nil
	}
	if !machineIdentityCredentialAccepted(ctx, identity, deps, environment) {
		return provisionedMachineIdentity{}, false, nil
	}
	return provisionedMachineIdentity{
		provider:     machineIdentityProviderConfig(host, identity),
		clientSecret: identity.ClientSecret,
		subject:      identity.Subject,
	}, true, nil
}

// machineIdentityPlatformClient authenticates as this host's alias; nothing
// about it reaches the environment.
func machineIdentityPlatformClient(ctx core.Context, store config.CloudReadStore, deps cloud.CloudDependencies) (*platformclient.PlatformClient, model.CloudProviderConfig, bool, error) {
	provider, _, ok, err := deploy.ResolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if err != nil && !deploy.HostAliasSelected(ctx.PlatformAlias) && deploy.IsPlatformAliasAmbiguity(err) {
		// Ambiguous aliases: fall back; the delegated path reports the ambiguity.
		return nil, model.CloudProviderConfig{}, false, nil
	}
	if err != nil {
		return nil, model.CloudProviderConfig{}, false, err
	}
	if !ok {
		// Silent, like the delegated path: its fallback already reports this.
		return nil, model.CloudProviderConfig{}, false, nil
	}
	client, provider, err := cloud.NewPlatformClientForAlias(ctx, store, provider.Alias, deps)
	if err != nil {
		return nil, model.CloudProviderConfig{}, false, err
	}
	return client, provider, true, nil
}

func fetchMachineIdentity(ctx core.Context, client *platformclient.PlatformClient, environment string) (platformclient.PlatformMachineIdentity, bool) {
	environmentID, err := cloud.ResolvePlatformEnvironmentID(context.Background(), client, environment)
	if err != nil {
		ctx.Trace("machine identity: " + err.Error() + "; leaving the environment's platform identity as it is")
		return platformclient.PlatformMachineIdentity{}, false
	}
	identity, err := client.MintMachineIdentity(context.Background(), environmentID)
	if err != nil {
		ctx.Trace("machine identity: the platform would not mint one for " + environment + " (" + err.Error() + "); leaving its platform identity as it is")
		return platformclient.PlatformMachineIdentity{}, false
	}
	if strings.TrimSpace(identity.ClientID) == "" || strings.TrimSpace(identity.ClientSecret) == "" {
		ctx.Trace("machine identity: the platform returned no usable credential for " + environment + "; leaving its platform identity as it is")
		return platformclient.PlatformMachineIdentity{}, false
	}
	return identity, true
}

// machineIdentityProviderConfig keeps the host alias's name and API URL, so the
// pod still holds exactly one erun alias, but uses the identity's own issuer.
func machineIdentityProviderConfig(host model.CloudProviderConfig, identity platformclient.PlatformMachineIdentity) model.CloudProviderConfig {
	provider := host
	provider.OIDCIssuerURL = strings.TrimSpace(identity.Issuer)
	provider.ERun = &model.ERunProviderConfig{
		APIURL:          host.ERun.APIURL,
		ClientID:        strings.TrimSpace(identity.ClientID),
		ClientSecretRef: machineIdentityClientSecretRef(host.Alias),
	}
	return cloud.NormalizeCloudProviderConfig(provider)
}

func machineIdentityCredentialAccepted(ctx core.Context, identity platformclient.PlatformMachineIdentity, deps cloud.CloudDependencies, environment string) bool {
	if err := verifyMachineIdentityCredential(ctx, identity, deps); err != nil {
		ctx.Trace("machine identity: the credential the platform minted for " + environment + " could not be exchanged for a token (" + err.Error() + "); leaving its platform identity as it is")
		return false
	}
	return true
}

func verifyMachineIdentityCredential(ctx core.Context, identity platformclient.PlatformMachineIdentity, deps cloud.CloudDependencies) error {
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
