package deploy

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// renderImagePullSecret wraps dockerConfigJSON in a standard dockerconfigjson
// Secret manifest, the same shape `kubectl create secret docker-registry`
// produces.
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

// refreshImagePullSecrets re-mints every dockerconfigjson Secret named in
// deployInput.ImagePullSecrets, from credentials resolved host-side for every
// registry the deploy is about to pull an image from. An ECR authorization
// token expires after twelve hours, so a pull secret named once via
// `--image-pull-secret` would otherwise rot. This runs before every rollout,
// so the pod a redeploy creates always starts with a fresh credential.
//
// The registry a component's imageOverrides names can differ from
// containerRegistry (a runtime image built into a BUILD-role registry while
// the chart itself is pulled from a DEPLOY-role one) — the pull secret must
// cover both, or the pod pulls from a registry the refreshed Secret carries no
// credential for at all. imagePullSecretRegistryHosts names every host in
// play; each is resolved independently so one registry's credential is not
// held hostage by another's.
//
// Skips entirely when the env names no pull secret at all -- nothing to
// refresh, so a public-image env stays byte-for-byte unchanged -- or when
// none of the registries in play name a host of their own (a bare Docker Hub
// namespace), or when no host resolved a credential at all, in which case
// nothing is read or applied and every named Secret is left exactly as it
// was. When at least one host resolves, each named Secret is read back (real
// runs only -- see applyImagePullSecrets for the dry-run trace) and only the
// resolved hosts' entries are overlaid onto it (mergeImagePullSecretAuths): a
// host that is known but did not resolve this run (no aws/docker session on
// the machine running the deploy) keeps its existing coverage in the Secret
// exactly as it was, the same fallback provisionRegistryCredentialSecret uses
// for the ghcr registry-credential secret; the other hosts' credentials still
// refresh.
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

// resolveImagePullSecretCredentials resolves a credential for every registry
// host the deploy's images actually pull from, tracing (and skipping) any
// host none resolves for rather than failing the whole refresh over it.
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

// mergeImagePullSecretAuths overlays this run's resolved credentials onto a
// Secret's existing auths, leaving every host neither side names untouched.
// A host with no existing entry and no resolved credential simply stays
// absent -- this never invents an empty or placeholder entry.
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

// applyImagePullSecrets re-applies every named dockerconfigjson Secret,
// merging this run's resolved credentials into whatever the Secret already
// covers (see existingImagePullSecretAuths / mergeImagePullSecretAuths)
// instead of replacing the whole auths document. Real mode reads the Secret
// back and applies the merged result; dry-run traces both commands it would
// run but executes neither, stating the merge as conditional on what the get
// above would find (see existingImagePullSecretAuths for why).
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

// imagePullSecretRegistryHosts names every registry host a deploy's images
// actually pull from: containerRegistry (what the chart itself resolves
// against) plus each imageOverrides entry's own registry when it names one.
// A component's image override is a full reference (host/name:tag), so its
// registry is read the same way resolveDeployRuntimeImage builds one, then
// reduced to the bare host the credential resolvers key on. Order is
// deterministic (sortedStringMapKeys) so the trace and the rendered
// dockerconfigjson auths are stable across runs.
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
