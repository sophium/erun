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

// registryCredentialSecretName names the Secret `erun init` mints from the
// operator's ghcr.io credential for pods that never authenticated themselves.
func registryCredentialSecretName(tenant string) string {
	return config.RuntimeReleaseName(tenant) + "-registry-credential"
}

// ghcrCredentialOwner returns the namespace owner ("ghcr.io/sophium" -> "sophium").
func ghcrCredentialOwner(registry string) string {
	_, owner, ok := strings.Cut(strings.TrimSpace(registry), "/")
	if !ok {
		return ""
	}
	return owner
}

// resolveHostGHCRCredentials keys resolved host credentials by registry host,
// omitting those that did not resolve.
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

// DockerConfigJSONAuthEntriesForCredentials encodes credentials as dockerconfigjson auth entries.
func DockerConfigJSONAuthEntriesForCredentials(credentials map[string]RegistryBasicAuth) map[string]config.DockerConfigJSONAuthEntry {
	entries := make(map[string]config.DockerConfigJSONAuthEntry, len(credentials))
	for host, auth := range credentials {
		entries[host] = config.DockerConfigJSONAuthEntry{Auth: base64.StdEncoding.EncodeToString([]byte(auth.Username + ":" + auth.Secret))}
	}
	return entries
}

// DockerConfigJSONForAuthEntries renders a .dockerconfigjson from already-encoded entries.
func DockerConfigJSONForAuthEntries(entries map[string]config.DockerConfigJSONAuthEntry) (string, error) {
	data, err := json.Marshal(config.DockerConfigJSONFile{Auths: entries})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func dockerConfigJSONForCredentials(credentials map[string]RegistryBasicAuth) (string, error) {
	return DockerConfigJSONForAuthEntries(DockerConfigJSONAuthEntriesForCredentials(credentials))
}

// renderRegistryCredentialSecret matches `kubectl create secret docker-registry` output.
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

// ProvisionRegistryCredentialSecret applies a dockerconfigjson Secret from host
// ghcr.io credentials and returns its name, or "" leaving the pod untouched.
// Resolution is local and read-only, so it runs under dry-run too.
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
