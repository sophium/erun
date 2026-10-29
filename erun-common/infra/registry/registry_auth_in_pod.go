package registry

import (
	"os"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/containerregistry"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
)

// InPodDeclaredRegistryAuth holds credentials from the env's own declared
// imagePullSecrets, consulted last and only for a deploy inside that env's pod.
var InPodDeclaredRegistryAuth map[string]RegistryBasicAuth

// readDeclaredImagePullSecretAuths is a test seam.
var readDeclaredImagePullSecretAuths = func(namespace, kubernetesContext, name string) (map[string]containerregistry.DockerConfigJSONAuthEntry, error) {
	return ExistingImagePullSecretAuths(kubernetesContext, namespace, name, ImagePullSecretGetArgs(namespace, kubernetesContext, name))
}

// ConfigureInPodDeclaredRegistryAuth loads the env's declared pull-secret
// credentials, since a runtime pod has no docker config, gh session or token and
// anonymous reads are inconclusive. Real runs in the target env's own pod only.
func ConfigureInPodDeclaredRegistryAuth(ctx core.Context, target config.OpenResult) {
	InPodDeclaredRegistryAuth = nil
	if ctx.DryRun {
		return
	}
	if !runningInTargetRuntimePod(target) {
		return
	}
	names := containerregistry.NormalizeImagePullSecrets(target.EnvConfig.ImagePullSecrets)
	credentials := readDeclaredRegistryCredentials(ctx, inPodNamespace(target), target.EnvConfig.KubernetesContext, names)
	if len(credentials) == 0 {
		return
	}
	InPodDeclaredRegistryAuth = credentials
	ctx.Trace("registry credential: resolved " + strconv.Itoa(len(credentials)) +
		" registry credential(s) from this environment's declared image pull secret(s) " +
		strings.Join(names, ", ") + " (credential redacted)")
}

func runningInTargetRuntimePod(target config.OpenResult) bool {
	tenant, environment, inPod := core.InjectedRuntimePodIdentity(os.Getenv)
	return inPod && tenant == strings.TrimSpace(target.Tenant) && environment == strings.TrimSpace(target.Environment)
}

// inPodNamespace prefers the chart's ERUN_NAMESPACE so a renamed namespace still works.
func inPodNamespace(target config.OpenResult) string {
	if namespace := strings.TrimSpace(os.Getenv("ERUN_NAMESPACE")); namespace != "" {
		return namespace
	}
	return runtimetarget.KubernetesNamespaceName(target.Tenant, target.Environment)
}

// readDeclaredRegistryCredentials decodes each Secret's auths, first Secret wins a host.
// An unreadable Secret is traced and skipped, not fatal.
func readDeclaredRegistryCredentials(ctx core.Context, namespace, kubernetesContext string, names []string) map[string]RegistryBasicAuth {
	credentials := make(map[string]RegistryBasicAuth, len(names))
	for _, name := range names {
		auths, err := readDeclaredImagePullSecretAuths(namespace, kubernetesContext, name)
		if err != nil {
			ctx.Trace("registry credential: could not read declared image pull secret " + name + ": " + err.Error())
			continue
		}
		for host, entry := range auths {
			if _, exists := credentials[host]; exists {
				continue
			}
			if auth, ok := decodeInlineDockerAuth(entry.Auth); ok {
				credentials[host] = auth
			}
		}
	}
	return credentials
}

func inPodDeclaredRegistryAuthFor(host string) (RegistryBasicAuth, bool) {
	if len(InPodDeclaredRegistryAuth) == 0 {
		return RegistryBasicAuth{}, false
	}
	for _, key := range hostLookupKeys(host) {
		if auth, ok := InPodDeclaredRegistryAuth[key]; ok {
			return auth, true
		}
	}
	return RegistryBasicAuth{}, false
}
