package setup

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// remoteProvisionedSecrets is a struct so the two names cannot be swapped.
type remoteProvisionedSecrets struct {
	registryCredential string
	platformAlias      string
}

// resolveRegistryCredentialSecret mints the ghcr.io credential Secret that
// ensureRemoteRegistryCredentials checks for in-pod.
func (s BootstrapRunner) resolveRegistryCredentialSecret(target config.OpenResult) (string, error) {
	registries := ghcrRegistriesRequiringCredential(config.EffectiveEnvironmentContainerRegistries(target.EnvConfig))
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	return registrypkg.ProvisionRegistryCredentialSecret(s.Context, target.Tenant, namespace, target.EnvConfig.KubernetesContext, registries)
}

// resolvePlatformAliasSecret prefers the env's own machine identity, so its
// calls are attributable, and falls back to the host's signed-in alias with a
// trace.
func (s BootstrapRunner) resolvePlatformAliasSecret(target config.OpenResult) (string, error) {
	namespace := config.KubernetesNamespaceName(target.Tenant, target.Environment)
	// Use the runner's store, not the default one.
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

// ensureRemoteRegistryCredentials fails init when a build- or deploy-role
// ghcr.io registry has no credential source in the pod; validity is left to
// release's preflight.
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

// ghcrRegistriesRequiringCredential skips non-ghcr and cluster: registries.
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
		if !entry.HasRole(model.RegistryRoleBuild) && !entry.HasRole(model.RegistryRoleDeploy) {
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

// Dry-run cannot exec into a pod, so it reports configured.
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

// remoteGHCRCredentialCheckScript mirrors resolveGHCRBasicAuth's three sources
// in shell and prints 1/0 rather than failing.
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
