package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/strategicpatch"
	"sigs.k8s.io/yaml"
)

// kubectlSecretApplyExecutionOperation is the ExecutionModeFor/
// ExecutionModeReport key for the `kubectl [--context c] -n <ns> apply -f -`
// of a core/v1 Secret, run from five call sites that pipe an erun-rendered
// manifest on stdin: the image-pull Secret refresh (applyImagePullSecrets),
// the registry-credential Secret (provisionRegistryCredentialSecret), the
// Cloudflare token (applyCloudflareCredentialsSecret), the desktop MCP
// public key (applyMCPAuthSecret), and expose's DNS-01 broker token
// (applyDNS01TokenSecret).
//
// This is the first *mutating* operation on the switch, and it is the reason
// the obvious library implementation is not the one used. Server-side apply
// is not equivalent to what `kubectl apply` does: it records ownership under
// its own field manager as an Apply operation over the manifest's own fields
// (f:stringData), writes no last-applied-configuration annotation, and leaves
// that divergence behind permanently -- an object applied once in each mode
// ends up carrying two managedFields entries and a different ownership map
// than one only ever applied by kubectl. Since the mode is a runtime switch an
// operator can flip either way at any time, that would make the rendered
// `kubectl apply` line stop describing what actually reached the cluster,
// which is the one thing this whole mechanism exists to keep true.
//
// So the library path reproduces kubectl's *client-side* apply instead: the
// last-applied-configuration annotation plus a three-way strategic merge
// patch, under kubectl's own field manager. Verified against a live cluster
// before this landed -- create, update, and a kubectl -> library -> kubectl
// mode flip all leave the object byte-identical (including managedFields) to
// a history that only ever ran kubectl.
//
// Narrow on purpose, per the one-key-per-call-site convention: core/v1 Secret
// only. The other `kubectl apply` call sites need a different mechanism, not a
// wider version of this one -- EnsureKubernetesResourceQuota pipes a
// two-document manifest (ResourceQuota + LimitRange) and renders its flags in
// a different order, and expose's Issuer/Certificate are cert-manager custom
// resources, which have no typed patch metadata and so take kubectl's JSON
// merge patch fallback via a discovery-backed RESTMapper and dynamic client.
// All of them stay subprocess-only.
const kubectlSecretApplyExecutionOperation = "kubectl-secret-apply"

// lastAppliedConfigurationAnnotation is where kubectl records the manifest it
// last applied, so the next apply has an "original" to three-way merge
// against. Reproduced verbatim rather than renamed: an object erun applies is
// routinely also applied or inspected by a human running plain kubectl.
const lastAppliedConfigurationAnnotation = "kubectl.kubernetes.io/last-applied-configuration"

// clientSideApplyFieldManager is the manager name kubectl attributes a
// client-side apply to. The library path claims the same name deliberately --
// a different one would fork the object's ownership history the moment the
// mode was flipped, which is exactly the divergence this operation avoids.
const clientSideApplyFieldManager = "kubectl-client-side-apply"

// kubectlApplyStdinArgs is the single source of the `kubectl [--context c] -n
// <ns> apply -f -` argv, shared by every call site that pipes a manifest on
// stdin -- the five Secret applies this operation switches plus expose's
// Ingress, Issuer and Certificate, which stay subprocess-only but render the
// identical command. Reading the manifest from stdin keeps a temp-file path
// (and any credential in it) out of the argv, so the trace stays
// deterministic and secret-free.
func kubectlApplyStdinArgs(namespace, kubernetesContext string) []string {
	args := []string{}
	if ctxName := strings.TrimSpace(kubernetesContext); ctxName != "" {
		args = append(args, "--context", ctxName)
	}
	return append(args, "-n", strings.TrimSpace(namespace), "apply", "-f", "-")
}

// applySecretManifest dispatches to the subprocess or library path per the
// kubectl-secret-apply execution mode (see execution_mode.go). description
// names the Secret in the error the caller would otherwise have written
// itself, so both paths fail with the same prefix. args is the exact apply
// command already traced by the caller, used by the subprocess path only.
func applySecretManifest(contextName, namespace, description, manifest string, args []string) error {
	if currentExecutionMode(kubectlSecretApplyExecutionOperation) == ExecutionModeLibrary {
		if err := applySecretViaClientGo(contextName, namespace, manifest); err != nil {
			return fmt.Errorf("kubectl apply %s: %s", description, err)
		}
		return nil
	}
	cmd := Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(manifest)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("kubectl apply %s: %w: %s", description, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// applySecretViaClientGo is the library-backed alternative to shelling out to
// `kubectl apply -f -`, reproducing kubectl's client-side apply against the
// same manifest the subprocess path would pipe: create the object outright
// when it does not exist yet, otherwise three-way merge this manifest against
// the one the annotation says was applied last and the object as it stands
// now.
func applySecretViaClientGo(contextName, namespace, manifest string) error {
	document, name, err := decodeSecretManifest(manifest)
	if err != nil {
		return err
	}
	modified, err := annotatedApplyManifest(document)
	if err != nil {
		return err
	}
	clientset, err := kubernetesClientsetForContext(strings.TrimSpace(contextName))
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

// decodeSecretManifest reads the manifest the subprocess path would pipe to
// kubectl's stdin, refusing anything that is not exactly one core/v1 Secret.
// This operation is wired only to Secret call sites, so an unrecognized
// document means the caller is wrong, and applying a document whose kind was
// not established is worse than not applying it at all. It returns the parsed
// document as a map rather than a typed Secret because that is what kubectl
// itself carries: map keys marshal in sorted order and absent fields stay
// absent, where a typed struct would reorder them and emit a null
// creationTimestamp -- and those bytes are what the annotation records.
func decodeSecretManifest(manifest string) (map[string]any, string, error) {
	raw, err := yaml.YAMLToJSON([]byte(manifest))
	if err != nil {
		return nil, "", fmt.Errorf("the manifest does not parse as a single YAML document: %s", err)
	}
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, "", fmt.Errorf("the manifest is not a single object: %s", err)
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

// annotatedApplyManifest reproduces kubectl's own apply annotation: the
// manifest, encoded exactly as it is about to be sent, recorded in
// metadata.annotations so the next apply has an original to merge against.
// The forced (here usually empty) annotations map and the trailing newline
// are not cosmetic -- kubectl sets a non-nil map before encoding and its
// serializer appends the newline, and these bytes are read back verbatim as
// the next merge's original, so a byte that differs here is a byte that
// differs in every subsequent merge.
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

// threeWaySecretMergePatch computes the patch kubectl apply computes, from the
// manifest the annotation says was applied last, the one being applied now,
// and the object as it stands. overwrite matches kubectl apply's own
// --overwrite default: a field this manifest sets wins over a value some other
// writer put there.
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
