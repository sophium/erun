package eruncommon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// imagePullSecretApplyArgs mirrors cloudflareSecretApplyArgs / mcpAuthSecretApplyArgs
// / registryCredentialSecretApplyArgs: a manifest piped via stdin, never an argv
// element.
func imagePullSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
}

// imagePullSecretGetArgs reads a named Secret back as JSON, so its existing
// .dockerconfigjson can be merged into rather than replaced.
func imagePullSecretGetArgs(namespace, kubernetesContext, name string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "get", "secret", strings.TrimSpace(name), "-o", "json")
	return args
}

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
`, name, namespace, dockerConfigJSONSecretKey, dockerConfigJSON)
}

// refreshImagePullSecrets re-mints every dockerconfigjson Secret named in
// deployInput.ImagePullSecrets, from credentials resolved host-side for every
// registry the deploy is about to pull an image from. An ECR authorization
// token expires after twelve hours (#1256): an operator who named a pull
// secret once via `--image-pull-secret` had no way to keep its content live
// short of noticing the rot and recreating it by hand. This runs before every
// rollout, so the pod a redeploy creates always starts with a fresh
// credential instead of the one a previous manual fix wrote.
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
// was. When at least one host resolves, each named Secret is read back and
// only the resolved hosts' entries are overlaid onto it (mergeImagePullSecretAuths):
// a host that is known but did not resolve this run (no aws/docker session on
// the machine running the deploy) keeps its existing coverage in the Secret
// exactly as it was, the same fallback provisionRegistryCredentialSecret uses
// for the ghcr registry-credential secret; the other hosts' credentials still
// refresh.
func refreshImagePullSecrets(ctx Context, deployInput HelmDeploySpec) error {
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
func resolveImagePullSecretCredentials(ctx Context, deployInput HelmDeploySpec) map[string]registryBasicAuth {
	hosts := imagePullSecretRegistryHosts(deployInput)
	credentials := make(map[string]registryBasicAuth, len(hosts))
	var unresolved []string
	for _, host := range hosts {
		if auth, ok := resolveOCIRegistryBasicAuth(host); ok {
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

// existingImagePullSecretAuths reads a named dockerconfigjson Secret back and
// decodes its current auths document, so applyImagePullSecrets can overlay
// this run's resolved credentials onto it instead of replacing the whole
// document (see refreshImagePullSecrets). This is a read with no side effect,
// so it runs the same under dry-run as for real -- only the apply that
// follows it is conditional on ctx.DryRun.
//
// A Secret that does not exist yet reads as no existing coverage, exactly
// what a first deploy always saw. A Secret that exists but cannot be decoded
// -- an unreadable base64 value, or a value that is not the auths document
// shape -- is refused rather than silently rebuilt from the resolved subset
// alone, because guessing wrong here would destroy exactly the coverage this
// function exists to protect; a genuine cluster read failure (RBAC denial,
// unreachable API server) is refused for the same reason.
func existingImagePullSecretAuths(ctx Context, deployInput HelmDeploySpec, name string) (map[string]dockerConfigJSONAuthEntry, error) {
	ctx.Trace("image pull secret " + name + ": reading existing coverage before merge")
	args := imagePullSecretGetArgs(deployInput.Namespace, deployInput.KubernetesContext, name)
	output, err := Command("kubectl", args...).CombinedOutput()
	if err != nil {
		if KubernetesResourceNotFound(string(output)) {
			return nil, nil
		}
		return nil, fmt.Errorf("read existing image pull secret %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: could not parse the existing secret", name)
	}
	encoded, ok := secret.Data[dockerConfigJSONSecretKey]
	if !ok {
		return nil, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s holds an unreadable value", name, dockerConfigJSONSecretKey)
	}
	var file dockerConfigJSONFile
	if err := json.Unmarshal(decoded, &file); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s does not decode as a docker config", name, dockerConfigJSONSecretKey)
	}
	return file.Auths, nil
}

// mergeImagePullSecretAuths overlays this run's resolved credentials onto a
// Secret's existing auths, leaving every host neither side names untouched.
// A host with no existing entry and no resolved credential simply stays
// absent -- this never invents an empty or placeholder entry.
func mergeImagePullSecretAuths(existing map[string]dockerConfigJSONAuthEntry, credentials map[string]registryBasicAuth) map[string]dockerConfigJSONAuthEntry {
	merged := make(map[string]dockerConfigJSONAuthEntry, len(existing)+len(credentials))
	for host, entry := range existing {
		merged[host] = entry
	}
	for host, entry := range dockerConfigJSONAuthEntriesForCredentials(credentials) {
		merged[host] = entry
	}
	return merged
}

// applyImagePullSecrets re-applies every named dockerconfigjson Secret,
// merging this run's resolved credentials into whatever the Secret already
// covers (see existingImagePullSecretAuths / mergeImagePullSecretAuths)
// instead of replacing the whole auths document, tracing each apply (dry-run
// stops before the write, not before the read).
func applyImagePullSecrets(ctx Context, deployInput HelmDeploySpec, credentials map[string]registryBasicAuth) error {
	applyArgs := imagePullSecretApplyArgs(deployInput.Namespace, deployInput.KubernetesContext)
	for _, name := range deployInput.ImagePullSecrets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		existing, err := existingImagePullSecretAuths(ctx, deployInput, name)
		if err != nil {
			return err
		}
		dockerConfigJSON, err := dockerConfigJSONForAuthEntries(mergeImagePullSecretAuths(existing, credentials))
		if err != nil {
			return fmt.Errorf("render image pull secret %s: %w", name, err)
		}
		ctx.Trace("apply image pull secret " + name + " (credential redacted)")
		ctx.TraceCommand("", "kubectl", applyArgs...)
		if ctx.DryRun {
			continue
		}
		manifest := renderImagePullSecret(name, deployInput.Namespace, dockerConfigJSON)
		cmd := Command("kubectl", applyArgs...)
		cmd.Stdin = strings.NewReader(manifest)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("kubectl apply image pull secret %s: %w: %s", name, err, strings.TrimSpace(string(out)))
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
		host, _, ok := ociRegistryHostFromNamespace(namespace)
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
		add(runtimeImageRegistry(deployInput.ImageOverrides[key]))
	}
	return hosts
}
