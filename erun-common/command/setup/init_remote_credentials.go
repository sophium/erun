package setup

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// remoteProvisionedSecrets names the credentials init resolved on the invoking
// host and minted into the environment's namespace. Both are returned together
// rather than as adjacent strings: the caller persists each onto its own
// EnvConfig field, and two bare strings at one call site is a swap waiting to
// happen.
type remoteProvisionedSecrets struct {
	registryCredential string
	platformAlias      string
}

// resolveRegistryCredentialSecret resolves the ghcr.io registries this env's
// build/deploy roles require a credential for and, when the host has one to
// give, mints the dockerconfigjson Secret the runtime chart mounts. It is the
// host side of ensureRemoteRegistryCredentials' in-pod check: that check can
// only fail loudly; this is what gives it something to find.
func (s BootstrapRunner) resolveRegistryCredentialSecret(target config.OpenResult) (string, error) {
	registries := ghcrRegistriesRequiringCredential(config.EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	return registrypkg.ProvisionRegistryCredentialSecret(s.Context, target.Tenant, namespace, target.EnvConfig.KubernetesContext, registries)
}

// resolvePlatformAliasSecret mints the Secret carrying the identity the runtime
// chart mounts for the pod's `erun` to read: the environment's own machine
// identity when the platform will mint one, and this host's own signed-in alias
// otherwise. Unlike the registry credential it is not gated on anything the
// environment declared -- any agent environment may be promoted to drive the
// merge queue, so the question is only whether the host has an identity to give.
//
// The machine identity is asked for first because it is the whole point of
// preferring one: an environment's platform calls are attributed to whoever ran
// init when they are made through a delegated session, and two environments
// provisioned from one host are indistinguishable in the audit trail. Every
// reason the platform will not mint one -- this host holds no alias to ask
// with, the environment is not registered yet, the tenant resolves by no issuer
// the platform administers, or the credential it returned does not exchange for
// a token -- falls back to that delegated session, which is exactly what the
// environment would have had before this existed. The fallbacks are traced, not
// silent: a deployment that stayed on the operator's identity has to be
// distinguishable from one that never asked.
func (s BootstrapRunner) resolvePlatformAliasSecret(target config.OpenResult) (string, error) {
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	// The runner's own store, so the alias is resolved against the same config
	// the rest of init reads rather than whatever the default store finds.
	var store config.CloudReadStore = config.ConfigStore{}
	if s.Store != nil {
		store = s.Store
	}
	deps := cloud.DefaultCloudDependencies()
	name, provisioned, err := provisionMachineIdentitySecret(s.Context, store, target.Tenant, target.Environment, namespace, target.EnvConfig.KubernetesContext, deps)
	if err != nil {
		return "", err
	}
	if provisioned {
		return name, nil
	}
	return deploy.ProvisionPlatformAliasSecret(s.Context, store, target.Tenant, namespace, target.EnvConfig.KubernetesContext, deps)
}

// ensureRemoteRegistryCredentials fails init when the pod it just deployed has
// no way to authenticate to a ghcr.io registry the env is configured to build
// to or deploy from. Left unchecked, a build-role registry with no credential
// is only discovered after a full multi-arch release build spends itself at
// the push (MissingGHCRCredentialError, checked again by release's own
// preflight), and a deploy-role registry with no credential leaves every
// registry read this pod makes unable to tell "denied" from "not published"
// (#1193). This only proves a credential source EXISTS in the pod -- a docker
// config entry, a gh session, or GH_TOKEN/GITHUB_TOKEN -- not that it is valid
// or correctly scoped; release's own preflight still confirms that before
// spending a build.
func (s BootstrapRunner) ensureRemoteRegistryCredentials(target config.OpenResult, req config.ShellLaunchParams) error {
	registries := ghcrRegistriesRequiringCredential(config.EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	for _, registry := range registries {
		configured, err := s.remoteGHCRCredentialConfigured(req, registry)
		if err != nil {
			return fmt.Errorf("check registry credentials for %s: %w", registry, err)
		}
		if !configured {
			return &registrypkg.MissingGHCRCredentialError{Registry: registry}
		}
	}
	return nil
}

// ghcrRegistriesRequiringCredential returns the distinct ghcr.io registries in
// list carrying the build or deploy role, in list order. Both roles need this
// pod to authenticate to ghcr.io itself -- build to push, deploy to read the
// images and runtime chart it resolves -- so a registry with neither role, a
// non-ghcr registry (a separate credential story this check does not police),
// or a cluster: entry (the pod never leaves the cluster to reach it) is not
// checked.
func ghcrRegistriesRequiringCredential(list config.ContainerRegistries) []string {
	registries := make([]string, 0, len(list))
	seen := make(map[string]struct{}, len(list))
	for _, entry := range list {
		if entry.Cluster != nil {
			continue
		}
		registry := strings.TrimSpace(entry.Registry)
		if registry == "" || !registrypkg.IsGHCRRegistry(registry) {
			continue
		}
		if !entry.HasRole(config.RegistryRoleBuild) && !entry.HasRole(config.RegistryRoleDeploy) {
			continue
		}
		if _, ok := seen[registry]; ok {
			continue
		}
		seen[registry] = struct{}{}
		registries = append(registries, registry)
	}
	return registries
}

// remoteGHCRCredentialConfigured execs into the pod to check for a ghcr.io
// credential. Dry-run does not exec into a real pod, so it reports configured
// rather than failing a plan preview over pod state a preview cannot know.
func (s BootstrapRunner) remoteGHCRCredentialConfigured(req config.ShellLaunchParams, registry string) (bool, error) {
	output, err := s.runRemoteScript(req, "registry-credential-check", remoteGHCRCredentialCheckScript(registry))
	if err != nil {
		return false, fmt.Errorf("%w%s", err, kube.FormatRemoteCommandStderr(output.Stderr))
	}
	if s.Context.DryRun {
		return true, nil
	}
	return strings.TrimSpace(output.Stdout) == "1", nil
}

// remoteGHCRCredentialCheckScript mirrors resolveGHCRBasicAuth's three routes
// (erun-common/registry_auth.go) in shell: that Go logic runs wherever the
// pod's own erun binary executes and cannot be invoked remotely, so this
// checks the same three sources directly -- a docker config entry for the
// registry host, a gh session, or GH_TOKEN/GITHUB_TOKEN. It reports 1/0 on
// stdout rather than failing the script itself, so a missing credential is a
// normal result init can act on rather than a script error.
func remoteGHCRCredentialCheckScript(registry string) string {
	host, _, _ := strings.Cut(registry, "/")
	return strings.Join([]string{
		"set -eu",
		"found=0",
		fmt.Sprintf("if [ -f \"$HOME/.docker/config.json\" ] && grep -q %s \"$HOME/.docker/config.json\" 2>/dev/null; then found=1; fi", core.ShellQuote(host)),
		"if [ \"$found\" -eq 0 ] && command -v gh >/dev/null 2>&1 && gh auth token -h github.com >/dev/null 2>&1; then found=1; fi",
		"if [ \"$found\" -eq 0 ] && { [ -n \"${GH_TOKEN:-}\" ] || [ -n \"${GITHUB_TOKEN:-}\" ]; }; then found=1; fi",
		"printf '%s\\n' \"$found\"",
	}, "\n")
}
