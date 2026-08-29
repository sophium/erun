package eruncommon

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

// registryCredentialSecretName derives the per-release Secret `erun init`
// mints from the operator's own resolved ghcr.io credential, so a build- or
// deploy-role pod that has never authenticated to its registry on its own can
// still read or push to it.
func registryCredentialSecretName(tenant string) string {
	return RuntimeReleaseName(tenant) + "-registry-credential"
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
func resolveHostGHCRCredentials(registries []string) map[string]registryBasicAuth {
	credentials := make(map[string]registryBasicAuth, len(registries))
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

// dockerConfigJSONSecretKey is the auths-document key kubectl itself uses in a
// dockerconfigjson Secret (`kubectl create secret docker-registry`), shared by
// every Secret this package renders or reads back in that shape.
const dockerConfigJSONSecretKey = ".dockerconfigjson"

type dockerConfigJSONAuthEntry struct {
	Auth string `json:"auth"`
}

type dockerConfigJSONFile struct {
	Auths map[string]dockerConfigJSONAuthEntry `json:"auths"`
}

// dockerConfigJSONAuthEntriesForCredentials encodes each resolved credential
// into the auth entry a dockerconfigjson Secret stores it as.
func dockerConfigJSONAuthEntriesForCredentials(credentials map[string]registryBasicAuth) map[string]dockerConfigJSONAuthEntry {
	entries := make(map[string]dockerConfigJSONAuthEntry, len(credentials))
	for host, auth := range credentials {
		entries[host] = dockerConfigJSONAuthEntry{Auth: base64.StdEncoding.EncodeToString([]byte(auth.username + ":" + auth.secret))}
	}
	return entries
}

// dockerConfigJSONForAuthEntries renders a .dockerconfigjson payload from
// already-encoded auth entries, the shape a caller merging in an existing
// Secret's entries holds (those decode with their encoding already applied,
// unlike a freshly resolved registryBasicAuth).
func dockerConfigJSONForAuthEntries(entries map[string]dockerConfigJSONAuthEntry) (string, error) {
	data, err := json.Marshal(dockerConfigJSONFile{Auths: entries})
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// dockerConfigJSONForCredentials renders the .dockerconfigjson payload a
// dockerconfigjson Secret carries, in the same shape docker itself writes.
func dockerConfigJSONForCredentials(credentials map[string]registryBasicAuth) (string, error) {
	return dockerConfigJSONForAuthEntries(dockerConfigJSONAuthEntriesForCredentials(credentials))
}

// registryCredentialSecretApplyArgs mirrors cloudflareSecretApplyArgs /
// mcpAuthSecretApplyArgs: a manifest piped via stdin, never an argv element.
func registryCredentialSecretApplyArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
	return args
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

// provisionRegistryCredentialSecret resolves a host-side ghcr.io credential
// for each registry in registries, and -- when the host has at least one to
// give -- mints or refreshes a dockerconfigjson Secret in the env's namespace
// via `kubectl apply -f -`. It returns the secret name to record on the env,
// or "" when the host had nothing to provision, in which case any existing
// in-pod credential is left exactly as ensureRemoteRegistryCredentials found
// it. Host credential resolution is a local, read-only lookup (docker config /
// gh session / GH_TOKEN), so it still runs under dry-run: the decision it
// makes belongs in the trace either way.
func provisionRegistryCredentialSecret(ctx Context, tenant, namespace, kubernetesContext string, registries []string) (string, error) {
	if len(registries) == 0 {
		return "", nil
	}
	credentials := resolveHostGHCRCredentials(registries)
	if len(credentials) == 0 {
		ctx.Trace("registry credential: no host credential resolved for " + strings.Join(registries, ", ") + "; leaving the pod's own credential state untouched")
		return "", nil
	}
	name := registryCredentialSecretName(tenant)
	args := registryCredentialSecretApplyArgs(namespace, kubernetesContext)
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
	cmd := Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("kubectl apply registry credential secret: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return name, nil
}
