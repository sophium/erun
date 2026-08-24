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
// deployInput.ImagePullSecrets, from a credential resolved host-side for the
// registry the deploy is about to pull the runtime image from. An ECR
// authorization token expires after twelve hours (#1256): an operator who
// named a pull secret once via `--image-pull-secret` had no way to keep its
// content live short of noticing the rot and recreating it by hand. This runs
// before every rollout, so the pod a redeploy creates always starts with a
// fresh credential instead of the one a previous manual fix wrote.
//
// Skips entirely when the env names no pull secret at all -- nothing to
// refresh, so a public-image env stays byte-for-byte unchanged -- or when the
// deploy registry does not name a host of its own (a bare Docker Hub
// namespace). When a host is known but no credential resolves for it (no
// aws/docker session on the machine running the deploy), the existing
// Secret's content is left exactly as it was, the same fallback
// provisionRegistryCredentialSecret uses for the ghcr registry-credential
// secret.
func refreshImagePullSecrets(ctx Context, deployInput HelmDeploySpec) error {
	if len(deployInput.ImagePullSecrets) == 0 {
		return nil
	}
	host, _, ok := ociRegistryHostFromNamespace(deployInput.ContainerRegistry)
	if !ok {
		return nil
	}
	auth, ok := resolveOCIRegistryBasicAuth(host)
	if !ok {
		ctx.Trace("image pull secret: no host credential resolved for " + host + "; leaving " + strings.Join(deployInput.ImagePullSecrets, ", ") + " untouched")
		return nil
	}
	dockerConfigJSON, err := dockerConfigJSONForCredentials(map[string]registryBasicAuth{host: auth})
	if err != nil {
		return fmt.Errorf("render image pull secret: %w", err)
	}
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
