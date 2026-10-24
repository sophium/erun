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

// ImagePullSecretGetArgs reads a Secret as JSON so its auths can be merged, not replaced.
func ImagePullSecretGetArgs(namespace, kubernetesContext, name string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	args = append(args, "-n", strings.TrimSpace(namespace), "get", "secret", strings.TrimSpace(name), "-o", "json")
	return args
}

// kubectlSecretGetExecutionOperation keys the execution mode for the Secret read
// that precedes each image-pull-secret refresh.
const kubectlSecretGetExecutionOperation = "kubectl-secret-get"

// ExistingImagePullSecretAuths reads a dockerconfigjson Secret so this run's
// credentials overlay it. Real runs only: a dry run traces the merge as conditional.
func ExistingImagePullSecretAuths(contextName, namespace, name string, args []string) (map[string]config.DockerConfigJSONAuthEntry, error) {
	if config.CurrentExecutionMode(kubectlSecretGetExecutionOperation) == config.ExecutionModeLibrary {
		return kube.LibraryImagePullSecretAuths(contextName, namespace, name)
	}
	return defaultExistingImagePullSecretAuths(name, args)
}

// defaultExistingImagePullSecretAuths treats a missing Secret as empty, but refuses
// an undecodable one or a failed read rather than destroying existing coverage.
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
