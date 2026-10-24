package registry

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// registryCredentialSecretName derives the per-release Secret `erun init`
// mints from the operator's own resolved ghcr.io credential, so a build- or
// deploy-role pod that has never authenticated to its registry on its own can
// still read or push to it.
func registryCredentialSecretName(tenant string) string {
	return config.RuntimeReleaseName(tenant) + "-registry-credential"
}

// ghcrCredentialOwner extracts the namespace owner from a bare registry entry
// (e.g. "ghcr.io/sophium" -> "sophium"), mirroring the owner scoping
// resolveGHCRBasicAuth expects. A registry with no namespace segment (bare
// "ghcr.io") has no owner to scope the gh lookup to.
func ghcrCredentialOwner(registry string) string {
	_, owner, ok := strings.Cut(strings.TrimSpace(registry), "/")
	if !ok {
		return ""
	}
	return owner
}

// resolveHostGHCRCredentials resolves a host-side (docker config / gh session
// / GH_TOKEN) credential for each of the given ghcr.io registries, keyed by
// host. Only registries a credential actually resolved for are present in the
// result, so a partial resolution still provisions what it can.
func resolveHostGHCRCredentials(registries []string) map[string]RegistryBasicAuth {
	credentials := make(map[string]RegistryBasicAuth, len(registries))
	for _, registry := range registries {
		host, _, _ := strings.Cut(registry, "/")
		if host == "" {
			continue
		}
		if _, exists := credentials[host]; exists {
			continue
		}
		auth, ok := resolveGHCRBasicAuth(ghcrCredentialOwner(registry))
		if !ok {
			continue
		}
		credentials[host] = auth
	}
	return credentials
}

// DockerConfigJSONAuthEntriesForCredentials encodes each resolved credential
// into the auth entry a dockerconfigjson Secret stores it as.
func DockerConfigJSONAuthEntriesForCredentials(credentials map[string]RegistryBasicAuth) map[string]config.DockerConfigJSONAuthEntry {
	entries := make(map[string]config.DockerConfigJSONAuthEntry, len(credentials))
	for host, auth := range credentials {
		entries[host] = config.DockerConfigJSONAuthEntry{Auth: base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Secret))}
	}
	return entries
}

// DockerConfigJSONForAuthEntries renders a .dockerconfigjson payload from
// already-encoded auth entries, the shape a caller merging in an existing
// Secret's entries holds (those decode with their encoding already applied,
// unlike a freshly resolved registryBasicAuth).
func DockerConfigJSONForAuthEntries(entries map[string]config.DockerConfigJSONAuthEntry) (string, error) {
	data, err := json.Marshal(config.DockerConfigJSONFile{Auths: entries})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// dockerConfigJSONForCredentials renders the .dockerconfigjson payload a
// dockerconfigjson Secret carries, in the same shape docker itself writes.
func dockerConfigJSONForCredentials(credentials map[string]RegistryBasicAuth) (string, error) {
	return DockerConfigJSONForAuthEntries(DockerConfigJSONAuthEntriesForCredentials(credentials))
}

// renderRegistryCredentialSecret wraps dockerConfigJSON in a standard
// dockerconfigjson Secret manifest, the same shape `kubectl create secret
// docker-registry` produces, so any tool that understands one understands the
// other.
func renderRegistryCredentialSecret(name, namespace, dockerConfigJSON string) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    app.kubernetes.io/managed-by: erun-init
type: kubernetes.io/dockerconfigjson
stringData:
  ".dockerconfigjson": %q
`, name, namespace, dockerConfigJSON)
}

// ProvisionRegistryCredentialSecret resolves a host-side ghcr.io credential
// for each registry in registries, and -- when the host has at least one to
// give -- mints or refreshes a dockerconfigjson Secret in the env's namespace
// via `kubectl apply -f -`. It returns the secret name to record on the env,
// or "" when the host had nothing to provision, in which case any existing
// in-pod credential is left exactly as ensureRemoteRegistryCredentials found
// it. Host credential resolution is a local, read-only lookup (docker config /
// gh session / GH_TOKEN), so it still runs under dry-run: the decision it
// makes belongs in the trace either way.
func ProvisionRegistryCredentialSecret(ctx core.Context, tenant, namespace, kubernetesContext string, registries []string) (string, error) {
	if len(registries) == 0 {
		return "", nil
	}
	credentials := resolveHostGHCRCredentials(registries)
	if len(credentials) == 0 {
		ctx.Trace("registry credential: no host credential resolved for " + strings.Join(registries, ", ") + "; leaving the pod's own credential state untouched")
		return "", nil
	}
	name := registryCredentialSecretName(tenant)
	args := kube.KubectlApplyStdinArgs(namespace, kubernetesContext)
	ctx.Trace("apply registry credential secret " + name + " (credential redacted)")
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return name, nil
	}
	dockerConfigJSON, err := dockerConfigJSONForCredentials(credentials)
	if err != nil {
		return "", fmt.Errorf("render registry credential secret: %w", err)
	}
	manifest := renderRegistryCredentialSecret(name, namespace, dockerConfigJSON)
	if err := kube.ApplySecretManifest(kubernetesContext, namespace, "registry credential secret", manifest, args); err != nil {
		return "", err
	}
	return name, nil
}
