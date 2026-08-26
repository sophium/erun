package eruncommon

import (
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
  ".dockerconfigjson": %q
`, name, namespace, dockerConfigJSON)
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
// namespace). When a host is known but no credential resolves for it (no
// aws/docker session on the machine running the deploy), that host's existing
// coverage in the Secret is left exactly as it was, the same fallback
// provisionRegistryCredentialSecret uses for the ghcr registry-credential
// secret; the other hosts' credentials still refresh.
func refreshImagePullSecrets(ctx Context, deployInput HelmDeploySpec) error {
	if len(deployInput.ImagePullSecrets) == 0 {
		return nil
	}
	credentials := resolveImagePullSecretCredentials(ctx, deployInput)
	if len(credentials) == 0 {
		return nil
	}
	dockerConfigJSON, err := dockerConfigJSONForCredentials(credentials)
	if err != nil {
		return fmt.Errorf("render image pull secret: %w", err)
	}
	return applyImagePullSecrets(ctx, deployInput, dockerConfigJSON)
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
		ctx.Trace("image pull secret: no host credential resolved for " + strings.Join(unresolved, ", ") + "; leaving " + strings.Join(deployInput.ImagePullSecrets, ", ") + " uncovered for it")
	}
	return credentials
}

// applyImagePullSecrets re-applies every named dockerconfigjson Secret with
// the resolved credentials, tracing each apply (dry-run stops there).
func applyImagePullSecrets(ctx Context, deployInput HelmDeploySpec, dockerConfigJSON string) error {
	args := imagePullSecretApplyArgs(deployInput.Namespace, deployInput.KubernetesContext)
	for _, name := range deployInput.ImagePullSecrets {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		ctx.Trace("apply image pull secret " + name + " (credential redacted)")
		ctx.TraceCommand("", "kubectl", args...)
		if ctx.DryRun {
			continue
		}
		manifest := renderImagePullSecret(name, deployInput.Namespace, dockerConfigJSON)
		cmd := Command("kubectl", args...)
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
