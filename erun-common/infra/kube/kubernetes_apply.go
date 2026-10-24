package kube

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/yaml"
)

// kubectlSecretApplyExecutionOperation switches core/v1 Secret applies to the library.
// It reproduces kubectl's client-side apply (last-applied annotation, three-way strategic
// merge, kubectl's field manager), not server-side apply, so flipping modes leaves objects
// byte-identical. Other `kubectl apply` sites stay subprocess-only.
const kubectlSecretApplyExecutionOperation = "kubectl-secret-apply"

// lastAppliedConfigurationAnnotation matches kubectl's, since humans also apply these objects.
const lastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// clientSideApplyFieldManager matches kubectl's so mode flips do not fork ownership history.
const clientSideApplyFieldManager = "kubectl-client-side-apply"

// KubectlApplyStdinArgs is the shared apply argv; stdin keeps credentials out of argv and traces.
func KubectlApplyStdinArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	return append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
}

// SecretApplyCreateRaceAttempts bounds retries after losing a create race; one retry
// normally suffices because the loser then takes the update path.
const SecretApplyCreateRaceAttempts = 3

// ApplySecretManifest dispatches per the kubectl-secret-apply mode. args is used by the
// subprocess path only. AlreadyExists is retried: parallel deploy components apply the
// same shared Secret and the create-race loser must still apply its content.
func ApplySecretManifest(contextName, namespace, description, manifest string, args []string) error {
	var err error
	for attempt := 0; attempt < SecretApplyCreateRaceAttempts; attempt++ {
		var lostCreateRace bool
		lostCreateRace, err = applySecretOnce(contextName, namespace, description, manifest, args)
		if err == nil || !lostCreateRace {
			return err
		}
	}
	return err
}

// applySecretOnce reports whether the failure was a lost create race.
func applySecretOnce(contextName, namespace, description, manifest string, args []string) (lostCreateRace bool, err error) {
	if config.CurrentExecutionMode(kubectlSecretApplyExecutionOperation) == config.ExecutionModeLibrary {
		if err := applySecretViaClientGo(contextName, namespace, manifest); err != nil {
			return apierrors.IsAlreadyExists(err), fmt.Errorf("kubectl apply %s: %w", description, err)
		}
		return false, nil
	}
	cmd := core.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	out, err := cmd.CombinedOutput()
	if err != nil {
		text := strings.TrimSpace(string(out))
		return kubectlReportsAlreadyExists(text), fmt.Errorf("kubectl apply %s: %w: %s", description, err, text)
	}
	return false, nil
}

// kubectlReportsAlreadyExists matches the parenthesized reason, since the subprocess
// path has no Status object.
func kubectlReportsAlreadyExists(output string) bool {
	return strings.Contains(output, "(AlreadyExists)")
}

// applySecretViaClientGo creates the Secret or three-way merges against the last-applied annotation.
func applySecretViaClientGo(contextName, namespace, manifest string) error {
	document, name, err := decodeSecretManifest(manifest)
	if err != nil {
		return err
	}
	modified, err := annotatedApplyManifest(document)
	if err != nil {
		return err
	}
	clientset, err := KubernetesClientsetForContext(strings.TrimSpace(contextName))
	if err != nil {
		return err
	}
	secrets := clientset.CoreV1().Secrets(strings.TrimSpace(namespace))
	live, err := secrets.Get(context.Background(), name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		created := &corev1.Secret{}
		if err := json.Unmarshal(modified, created); err != nil {
			return err
		}
		_, err = secrets.Create(context.Background(), created, metav1.CreateOptions{FieldManager: clientSideApplyFieldManager})
		return err
	}
	if err != nil {
		return err
	}
	patch, err := threeWaySecretMergePatch(live, modified)
	if err != nil {
		return err
	}
	_, err = secrets.Patch(context.Background(), name, types.StrategicMergePatchType, patch,
		metav1.PatchOptions{FieldManager: clientSideApplyFieldManager})
	return err
}

// decodeSecretManifest accepts exactly one core/v1 Secret. It returns a map, as kubectl
// does, so the annotation bytes keep sorted keys and no null creationTimestamp.
func decodeSecretManifest(manifest string) (map[string]any, string, error) {
	raw, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		return nil, "", fmt.Errorf("the manifest does not parse as a single YAML document: %w", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, "", fmt.Errorf("the manifest is not a single object: %w", err)
	}
	apiVersion, _ := document["apiVersion"].(string)
	kind, _ := document["kind"].(string)
	if apiVersion != "v1" || kind != "Secret" {
		return nil, "", fmt.Errorf("the library path applies only v1 Secret manifests, but this one is %q/%q", apiVersion, kind)
	}
	metadata, _ := document["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	if strings.TrimSpace(name) == "" {
		return nil, "", fmt.Errorf("the manifest names no secret")
	}
	return document, name, nil
}

// annotatedApplyManifest matches kubectl's annotation byte for byte (non-nil annotations
// map, trailing newline), since it becomes the next merge's original.
func annotatedApplyManifest(document map[string]any) ([]byte, error) {
	metadata, ok := document["metadata"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("the manifest has no metadata")
	}
	annotations := map[string]any{}
	if existing, ok := metadata["annotations"].(map[string]any); ok {
		for key, value := range existing {
			if key != lastAppliedConfigurationAnnotation {
				annotations[key] = value
			}
		}
	}
	metadata["annotations"] = annotations
	recorded, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	annotations[lastAppliedConfigurationAnnotation] = string(recorded) + "\n"
	return json.Marshal(document)
}

// threeWaySecretMergePatch computes kubectl apply's patch with its --overwrite default.
func threeWaySecretMergePatch(live *corev1.Secret, modified []byte) ([]byte, error) {
	current, err := json.Marshal(live)
	if err != nil {
		return nil, err
	}
	patchMeta, err := strategicpatch.NewPatchMetaFromStruct(corev1.Secret{})
	if err != nil {
		return nil, err
	}
	original := []byte(live.Annotations[lastAppliedConfigurationAnnotation])
	return strategicpatch.CreateThreeWayMergePatch(original, modified, current, patchMeta, true)
}
