package eruncommon

import (
	"os"
	"strconv"
	"strings"
)

// inPodDeclaredRegistryAuth holds registry credentials resolved from the
// dockerconfigjson Secrets the environment running this process declared as its
// own imagePullSecrets. It is populated only for a deploy running inside that
// env's own runtime pod (configureInPodDeclaredRegistryAuth), and consulted as
// the last route of resolveGHCRBasicAuth / resolveOCIRegistryBasicAuth -- after
// the docker config, the gh session, and GH_TOKEN/GITHUB_TOKEN have all come up
// empty, which is exactly the state a runtime pod starts in.
//
// It is a location, not a new class of credential: the Secret is read with the
// same kubernetes dockerconfigjson read deploy already uses
// (existingImagePullSecretAuths) and decoded with the same inline-auth decoder
// (decodeInlineDockerAuth), and the credential it carries is literally the one
// the env declared for pulling its own images. Consulting it is what lets a
// private-chart read in the pod be definitive instead of anonymous; it never
// turns an indeterminate read into a confirmed one.
var inPodDeclaredRegistryAuth map[string]registryBasicAuth

// readDeclaredImagePullSecretAuths is a seam so tests exercise the resolution
// without a live cluster. Mirrors runECRLoginPassword / resolveGHCRTokenViaGH.
var readDeclaredImagePullSecretAuths = func(namespace, kubernetesContext, name string) (map[string]dockerConfigJSONAuthEntry, error) {
	return existingImagePullSecretAuths(kubernetesContext, namespace, name, imagePullSecretGetArgs(namespace, kubernetesContext, name))
}

// configureInPodDeclaredRegistryAuth resolves the registry credentials the
// environment running this process declared for its own runtime pod, so a
// registry read can authenticate with a credential that already exists rather
// than falling back to anonymous.
//
// A runtime pod starts with no docker config, no gh session, and no
// GH_TOKEN/GITHUB_TOKEN -- none of the three routes resolveGHCRBasicAuth knows
// -- yet its environment declares where the credential lives twice:
// `imagepullsecrets` names the dockerconfigjson Secret, and the
// `containerregistries` entry states which registry needs it. The pod's service
// account can read the Secret. Without this, every read of a private namespace
// is anonymous, ghcr refuses to mint a token, and because an inconclusive read
// is not evidence of absence, deploy refuses rather than guessing.
//
// Deliberately narrow: only in the target env's own runtime pod (the injected
// ERUN_TENANT/ERUN_ENVIRONMENT pair), only for the Secrets that env declared,
// and only on a real run -- a dry run states no cluster read here, the same
// tradeoff existingImagePullSecretAuths makes for the merged image-pull-secret
// read, so preview traces stay free of cluster state a preview cannot know.
func configureInPodDeclaredRegistryAuth(ctx Context, target OpenResult) {
	inPodDeclaredRegistryAuth = nil
	if ctx.DryRun {
		return
	}
	if !runningInTargetRuntimePod(target) {
		return
	}
	names := normalizeImagePullSecrets(target.EnvConfig.ImagePullSecrets)
	credentials := readDeclaredRegistryCredentials(ctx, inPodNamespace(target), target.EnvConfig.KubernetesContext, names)
	if len(credentials) == 0 {
		return
	}
	inPodDeclaredRegistryAuth = credentials
	ctx.Trace("registry credential: resolved " + strconv.Itoa(len(credentials)) +
		" registry credential(s) from this environment's declared image pull secret(s) " +
		strings.Join(names, ", ") + " (credential redacted)")
}

// runningInTargetRuntimePod reports whether this process is the target env's own
// runtime pod -- the only place the Secrets that env declares are both reachable
// and this process's own credential. A host run, or a pod deploying some other
// env, resolves exactly as it did before.
func runningInTargetRuntimePod(target OpenResult) bool {
	tenant, environment, inPod := injectedRuntimePodIdentity(os.Getenv)
	return inPod && tenant == strings.TrimSpace(target.Tenant) && environment == strings.TrimSpace(target.Environment)
}

// inPodNamespace names the namespace the pod runs in, preferring the chart's own
// projection over the derived name so a renamed namespace still reads its own
// declared Secrets.
func inPodNamespace(target OpenResult) string {
	if namespace := strings.TrimSpace(os.Getenv("ERUN_NAMESPACE")); namespace != "" {
		return namespace
	}
	return KubernetesNamespaceName(target.Tenant, target.Environment)
}

// readDeclaredRegistryCredentials reads each declared dockerconfigjson Secret
// and decodes its auths entries into per-host credentials, first named Secret
// winning a host. A Secret the pod cannot read is traced and skipped: the read
// is a resolution aid, so its failure leaves the run credential-less rather than
// failing the deploy on the read itself.
func readDeclaredRegistryCredentials(ctx Context, namespace, kubernetesContext string, names []string) map[string]registryBasicAuth {
	credentials := make(map[string]registryBasicAuth, len(names))
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

// inPodDeclaredRegistryAuthFor returns the declared-image-pull-secret credential
// for a registry host, keyed the same way docker keys its own config. False on
// every process that is not an in-pod deploy, and on any host the declared
// Secrets carry no entry for.
func inPodDeclaredRegistryAuthFor(host string) (registryBasicAuth, bool) {
	if len(inPodDeclaredRegistryAuth) == 0 {
		return registryBasicAuth{}, false
	}
	for _, key := range hostLookupKeys(host) {
		if auth, ok := inPodDeclaredRegistryAuth[key]; ok {
			return auth, true
		}
	}
	return registryBasicAuth{}, false
}
