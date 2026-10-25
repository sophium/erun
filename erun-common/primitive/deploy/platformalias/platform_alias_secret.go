package platformalias

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/cloud/secretstore"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/shared/cloudconfig"
	"gopkg.in/yaml.v3"
)

// Platform commands need an erun cloud alias, which only an interactive OIDC
// login creates, so `erun init` delegates the invoking host's own operator
// identity to the pod.

// PlatformAliasSecretName names the per-environment Secret `erun init` mints from the operator's alias.
func PlatformAliasSecretName(tenant string) string {
	return runtimetarget.RuntimeReleaseName(tenant) + "-platform-alias"
}

// The runtime chart mounts these keys at /etc/erun/platform-alias, where the runtime
// boot (syncRuntimePlatformAlias) turns them into the pod's config entry and secret file.
const (
	// PlatformAliasEntryKey holds one `cloudproviders:` list item, not the key:
	// a second key would be a duplicate mapping the config reader refuses.
	PlatformAliasEntryKey = "cloud-provider-entry.yaml"
	// PlatformAliasSecretFileKey holds the store's hashed file basename, so the pod never derives it.
	PlatformAliasSecretFileKey  = "cloud-secret-file"
	PlatformAliasSecretTokenKey = "cloud-secret-token"
)

// RenderPlatformAliasEntry renders the alias as a sequence item at indent 2:
// older runtime images append it under a 2-indented block, and mixed indents
// fail to parse.
func RenderPlatformAliasEntry(provider model.CloudProviderConfig) (string, error) {
	data, err := yaml.Marshal([]model.CloudProviderConfig{provider})
	if err != nil {
		return "", fmt.Errorf("render platform alias entry: %w", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	for i, line := range lines {
		lines[i] = "  " + line
	}
	return strings.Join(lines, "\n") + "\n", nil
}

// ResolveHostPlatformAlias resolves the named alias, else the host's sole erun alias,
// and its refresh token. err is set only for an unusable named alias or the unnamed
// several-aliases ambiguity; other declines are silent.
func ResolveHostPlatformAlias(store config.CloudReadStore, deps cloud.CloudDependencies, alias string) (model.CloudProviderConfig, string, bool, error) {
	explicit := HostAliasSelected(alias)
	if store == nil || deps.CloudSecretStore == nil {
		return model.CloudProviderConfig{}, "", false, hostAliasUnusable(
			explicit, alias, errors.New("this host has no readable cloud secret store, so the alias's signed-in session cannot be delivered to the environment"))
	}
	provider, err := cloudconfig.ResolveERunPlatformAlias(store, alias)
	if err != nil {
		return model.CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, err)
	}
	if provider.ERun == nil || strings.TrimSpace(provider.ERun.RefreshTokenRef) == "" {
		return model.CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, noStoredSessionError(provider.Alias))
	}
	token, err := deps.CloudSecretStore.LoadCloudSecret(provider.ERun.RefreshTokenRef)
	if err != nil || strings.TrimSpace(token) == "" {
		return model.CloudProviderConfig{}, "", false, hostAliasUnusable(explicit, alias, noStoredSessionError(provider.Alias))
	}
	return provider, token, true, nil
}

// HostAliasSelected reports whether the operator named the alias themselves.
func HostAliasSelected(alias string) bool {
	return strings.TrimSpace(alias) != ""
}

// hostAliasUnusable fails a named alias, but returns the several-aliases ambiguity
// for an unnamed one because the operator can lift it; other causes are nil.
func hostAliasUnusable(explicit bool, alias string, cause error) error {
	if explicit {
		return fmt.Errorf("--erun-alias %q: %w", alias, cause)
	}
	if IsPlatformAliasAmbiguity(cause) {
		return cause
	}
	return nil
}

// platformAliasDeclineLine renders the decline line; callers that must stay silent
// do not call it.
func platformAliasDeclineLine(cause error) string {
	if cause == nil {
		return "platform alias: no signed-in erun cloud provider alias on this host; leaving the pod's own alias state untouched"
	}
	return "platform alias: " + cause.Error() +
		"; no alias was provisioned for this environment, so its pod cannot call the platform until a deploy names one"
}

func noStoredSessionError(alias string) error {
	return fmt.Errorf("this host holds no session for it; run `erun cloud login %s` first", alias)
}

// RenderPlatformAliasSecret renders an Opaque Secret; `kubectl apply` makes re-running init refresh it.
func RenderPlatformAliasSecret(name, namespace, entry, secretFile, token string) string {
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
		PlatformAliasEntryKey, entry,
		PlatformAliasSecretFileKey, secretFile,
		PlatformAliasSecretTokenKey, token)
}

// ProvisionPlatformAliasSecret mints or refreshes the alias Secret and returns its
// name, or "" when the host had nothing to provision. The lookup is read-only, so
// it also runs under dry-run.
func ProvisionPlatformAliasSecret(ctx core.Context, store config.CloudReadStore, tenant, namespace, kubernetesContext string, deps cloud.CloudDependencies) (string, error) {
	provider, token, ok, cause := ResolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if !ok {
		if HostAliasSelected(ctx.PlatformAlias) {
			return "", cause
		}
		ctx.Trace(platformAliasDeclineLine(cause))
		return "", nil
	}
	entry, err := RenderPlatformAliasEntry(provider)
	if err != nil {
		return "", err
	}
	name := PlatformAliasSecretName(tenant)
	args := kube.KubectlApplyStdinArgs(namespace, kubernetesContext)
	ctx.Trace("apply platform alias secret " + name + " (alias " + provider.Alias + ", token redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return name, nil
	}
	manifest := RenderPlatformAliasSecret(name, namespace, entry, secretstore.CloudSecretFileName(provider.ERun.RefreshTokenRef), token)
	if err := kube.ApplySecretManifest(kubernetesContext, namespace, "platform alias secret", manifest, args); err != nil {
		return "", err
	}
	return name, nil
}

// ReconcilePlatformAliasSecret retrofits the alias onto an env that records none.
// An env that names one is left alone: the Secret carries the delegating operator's
// identity, so re-provisioning would silently change who the env acts as. Only the
// several-aliases ambiguity is traced; having nothing to give stays silent.
func ReconcilePlatformAliasSecret(ctx core.Context, deployInput *helmdeploy.HelmDeploySpec) error {
	if deployInput == nil || deployInput.ReleaseName != runtimetarget.RuntimeReleaseName(deployInput.Tenant) {
		return nil
	}
	if strings.TrimSpace(deployInput.PlatformAliasSecretName) != "" {
		if strings.TrimSpace(ctx.PlatformAlias) != "" {
			ctx.Trace("platform alias: this environment already records " + deployInput.PlatformAliasSecretName +
				", so --erun-alias " + ctx.PlatformAlias + " is unused; re-run `erun init` to re-provision it")
		}
		return nil
	}
	store, deps := config.ConfigStore{}, cloud.DefaultCloudDependencies()
	_, _, ok, cause := ResolveHostPlatformAlias(store, deps, ctx.PlatformAlias)
	if !ok {
		if HostAliasSelected(ctx.PlatformAlias) {
			return cause
		}
		if cause != nil {
			ctx.Trace(platformAliasDeclineLine(cause))
		}
		return nil
	}
	name, err := ProvisionPlatformAliasSecret(ctx, store, deployInput.Tenant, deployInput.Namespace, deployInput.KubernetesContext, deps)
	if err != nil {
		return err
	}
	if name == "" {
		return nil
	}
	// The chart mounts the Secret only when the deploy names it.
	deployInput.PlatformAliasSecretName = name
	return recordPlatformAliasSecretNameOnEnv(ctx, deployInput.Tenant, deployInput.Environment, name)
}

// recordPlatformAliasSecretNameOnEnv records the name before the rollout, like
// recordMCPAuthKeyOnEnv, so a later deploy cannot drop an existing mount.
func recordPlatformAliasSecretNameOnEnv(ctx core.Context, tenant, environment, name string) error {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return nil
	}
	envConfig, _, err := config.LoadEnvConfig(tenant, environment)
	loaded := err == nil
	if loaded && strings.TrimSpace(envConfig.PlatformAliasSecretName) == name {
		return nil
	}
	ctx.Trace("deploy: platform alias: recording secret " + name + " on " + tenant + "/" + environment)
	if ctx.DryRun {
		return nil
	}
	if !loaded {
		ctx.Trace("deploy: platform alias: could not read the env config of " + tenant + "/" + environment + " to record " + name)
		return nil
	}
	envConfig.PlatformAliasSecretName = name
	if err := config.SaveEnvConfig(tenant, envConfig); err != nil {
		return fmt.Errorf("record platform alias secret on %s/%s: %w", tenant, environment, err)
	}
	return nil
}
