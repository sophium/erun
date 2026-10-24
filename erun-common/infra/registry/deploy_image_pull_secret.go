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

// ImagePullSecretGetArgs reads a named Secret back as JSON, so its existing
// .dockerconfigjson can be merged into rather than replaced.
func ImagePullSecretGetArgs(namespace, kubernetesContext, name string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "get", "secret", strings.TrimSpace(name), "-o", "json")
	return args
}

// kubectlSecretGetExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `kubectl get secret <name> -o json` read
// (existingImagePullSecretAuths below), which applyImagePullSecrets runs
// ahead of every image-pull-secret refresh so this run's resolved
// credentials merge into the Secret's existing auths instead of replacing
// them outright. Picked next after kubectl-pvc-get for the same narrow
// shape -- a single resource kind (Secret), a single Get-by-name call, no
// streaming, no mutation -- reusing kubernetesClientsetForContext rather
// than adding new dependency surface. Unlike the namespace/PVC existence
// checks, what this Get returns is read for its content (the Secret's own
// dockerconfigjson), not just checked for presence; the Secret's own
// "apply" (mutation) side stays on the subprocess path.
const kubectlSecretGetExecutionOperation = "kubectl-secret-get"

// ExistingImagePullSecretAuths dispatches to the subprocess or library path
// per the kubectl-secret-get execution mode (see execution_mode.go), reading
// a named dockerconfigjson Secret back so applyImagePullSecrets can overlay
// this run's resolved credentials onto it instead of replacing the whole
// document (see refreshImagePullSecrets). Called only for a real run: a dry
// run has no cluster read to show without every dry-run scenario needing a
// kubectl stub it otherwise has no reason to declare, the same tradeoff
// TraceEnsureKubernetesNamespace makes for the namespace-exists check, so
// applyImagePullSecrets states the merge as conditional instead of asserting
// it. args is the exact get command already traced by the caller, used by
// the subprocess path only.
func ExistingImagePullSecretAuths(contextName, namespace, name string, args []string) (map[string]config.DockerConfigJSONAuthEntry, error) {
	if config.CurrentExecutionMode(kubectlSecretGetExecutionOperation) == config.ExecutionModeLibrary {
		return kube.LibraryImagePullSecretAuths(contextName, namespace, name)
	}
	return defaultExistingImagePullSecretAuths(name, args)
}

// defaultExistingImagePullSecretAuths is the subprocess-backed path
// existingImagePullSecretAuths dispatches to by default.
//
// A Secret that does not exist yet reads as no existing coverage, exactly
// what a first deploy always saw. A Secret that exists but cannot be decoded
// -- an unreadable base64 value, or a value that is not the auths document
// shape -- is refused rather than silently rebuilt from the resolved subset
// alone, because guessing wrong here would destroy exactly the coverage this
// function exists to protect; a genuine cluster read failure (RBAC denial,
// unreachable API server) is refused for the same reason.
func defaultExistingImagePullSecretAuths(name string, args []string) (map[string]config.DockerConfigJSONAuthEntry, error) {
	output, err := core.Command("kubectl", args...).CombinedOutput()
	if err != nil {
		if kube.KubernetesResourceNotFound(string(output)) {
			return nil, nil //nolint:nilnil // a nil map is the empty auths set: no existing coverage
		}
		return nil, fmt.Errorf("read existing image pull secret %s: %w: %s", name, err, strings.TrimSpace(string(output)))
	}
	var secret struct {
		Data map[string]string `json:"data"`
	}
	if err := json.Unmarshal(output, &secret); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: could not parse the existing secret", name)
	}
	encoded, ok := secret.Data[config.DockerConfigJSONSecretKey]
	if !ok {
		return nil, nil //nolint:nilnil // a nil map is the empty auths set: no existing coverage
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s holds an unreadable value", name, config.DockerConfigJSONSecretKey)
	}
	var file config.DockerConfigJSONFile
	if err := json.Unmarshal(decoded, &file); err != nil {
		return nil, fmt.Errorf("read existing image pull secret %s: %s does not decode as a docker config", name, config.DockerConfigJSONSecretKey)
	}
	return file.Auths, nil
}
