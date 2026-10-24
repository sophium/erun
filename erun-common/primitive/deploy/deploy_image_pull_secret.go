package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
)

func renderImagePullSecret(name, namespace, dockerConfigJSON string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-deploy
type: kubernetes.io/dockerconfigjson
stringData:
  %q: %q
`, name, namespace, config.DockerConfigJSONSecretKey, dockerConfigJSON)
}

// refreshImagePullSecrets re-mints the named pull Secrets before every rollout,
// since ECR tokens expire after twelve hours. Only resolved hosts are overlaid;
// an unresolved host keeps its existing entry.
func refreshImagePullSecrets(ctx core.Context, deployInput HelmDeploySpec) error {
	if len(deployInput.ImagePullSecrets) == 0 {
		return nil
	}
	credentials := resolveImagePullSecretCredentials(ctx, deployInput)
	if len(credentials) == 0 {
		return nil
	}
	return applyImagePullSecrets(ctx, deployInput, credentials)
}

func resolveImagePullSecretCredentials(ctx core.Context, deployInput HelmDeploySpec) map[string]registry.RegistryBasicAuth {
	hosts := imagePullSecretRegistryHosts(deployInput)
	credentials := make(map[string]registry.RegistryBasicAuth, len(hosts))
	var unresolved []string
	for _, host := range hosts {
		if auth, ok := registry.ResolveOCIRegistryBasicAuth(host); ok {
			credentials[host] = auth
		} else {
			unresolved = append(unresolved, host)
		}
	}
	if len(unresolved) > 0 {
		ctx.Trace("image pull secret: no host credential resolved for " + strings.Join(unresolved, ", ") + "; existing coverage for it in " + strings.Join(deployInput.ImagePullSecrets, ", ") + " is left unchanged")
	}
	return credentials
}

func mergeImagePullSecretAuths(existing map[string]config.DockerConfigJSONAuthEntry, credentials map[string]registry.RegistryBasicAuth) map[string]config.DockerConfigJSONAuthEntry {
	merged := make(map[string]config.DockerConfigJSONAuthEntry, len(existing)+len(credentials))
	for host, entry := range existing {
		merged[host] = entry
	}
	for host, entry := range registry.DockerConfigJSONAuthEntriesForCredentials(credentials) {
		merged[host] = entry
	}
	return merged
}

func applyImagePullSecrets(ctx core.Context, deployInput HelmDeploySpec, credentials map[string]registry.RegistryBasicAuth) error {
	applyArgs := kube.KubectlApplyStdinArgs(deployInput.Namespace, deployInput.KubernetesContext)
	for _, name := range deployInput.ImagePullSecrets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		getArgs := registry.ImagePullSecretGetArgs(deployInput.Namespace, deployInput.KubernetesContext, name)
		ctx.TraceCommand("", "kubectl", getArgs...)
		ctx.Trace("apply image pull secret " + name + " (credential redacted)")
		ctx.TraceCommand("", "kubectl", applyArgs...)
		if ctx.DryRun {
			ctx.Trace("image pull secret " + name + ": every host the read above already covers keeps its existing entry except the ones just resolved, which refresh")
			continue
		}
		existing, err := registry.ExistingImagePullSecretAuths(deployInput.KubernetesContext, deployInput.Namespace, name, getArgs)
		if err != nil {
			return err
		}
		dockerConfigJSON, err := registry.DockerConfigJSONForAuthEntries(mergeImagePullSecretAuths(existing, credentials))
		if err != nil {
			return fmt.Errorf("render image pull secret %s: %w", name, err)
		}
		manifest := renderImagePullSecret(name, deployInput.Namespace, dockerConfigJSON)
		if err := kube.ApplySecretManifest(deployInput.KubernetesContext, deployInput.Namespace, "image pull secret "+name, manifest, applyArgs); err != nil {
			return err
		}
	}
	return nil
}

// imagePullSecretRegistryHosts includes imageOverrides' registries, which can
// differ from containerRegistry.
func imagePullSecretRegistryHosts(deployInput HelmDeploySpec) []string {
	seen := make(map[string]struct{}, 2)
	hosts := make([]string, 0, 2)
	add := func(namespace string) {
		host, _, ok := config.OciRegistryHostFromNamespace(namespace)
		if !ok {
			return
		}
		if _, dup := seen[host]; dup {
			return
		}
		seen[host] = struct{}{}
		hosts = append(hosts, host)
	}
	add(deployInput.ContainerRegistry)
	for _, key := range sortedStringMapKeys(deployInput.ImageOverrides) {
		add(config.RuntimeImageRegistry(deployInput.ImageOverrides[key]))
	}
	return hosts
}
