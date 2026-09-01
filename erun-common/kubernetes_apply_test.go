package eruncommon

import (
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const probeSecretManifest = `apiVersion: v1
kind: Secret
metadata:
  name: team-devops-registry-credential
  namespace: team-dev
  labels:
    app.kubernetes.io/managed-by: erun-init
type: Opaque
stringData:
  token: "s3cret"
`

// The annotation bytes are read back verbatim as the next apply's merge
// original, so they are a contract with kubectl, not an implementation
// detail: kubectl records the manifest with sorted keys (it carries an
// unstructured map, not a typed struct), a forced annotations map, and the
// trailing newline its serializer appends. Any of those three drifting
// silently changes every subsequent three-way merge.
func TestAnnotatedApplyManifestMatchesKubectlsRecordedBytes(t *testing.T) {
	document, name, err := decodeSecretManifest(probeSecretManifest)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if name != "team-devops-registry-credential" {
		t.Fatalf("got name %q", name)
	}
	modified, err := annotatedApplyManifest(document)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	var applied corev1.Secret
	if err := json.Unmarshal(modified, &applied); err != nil {
		t.Fatalf("unmarshal applied: %v", err)
	}
	recorded := applied.Annotations[lastAppliedConfigurationAnnotation]
	want := `{"apiVersion":"v1","kind":"Secret","metadata":{"annotations":{},` +
		`"labels":{"app.kubernetes.io/managed-by":"erun-init"},` +
		`"name":"team-devops-registry-credential","namespace":"team-dev"},` +
		`"stringData":{"token":"s3cret"},"type":"Opaque"}` + "\n"
	if recorded != want {
		t.Fatalf("recorded configuration does not match what kubectl would record:\n got: %s\nwant: %s", recorded, want)
	}
}

// An annotation the manifest carries of its own must survive; only the
// last-applied key is replaced, exactly as kubectl does it.
func TestAnnotatedApplyManifestKeepsTheManifestsOwnAnnotations(t *testing.T) {
	document, _, err := decodeSecretManifest(`apiVersion: v1
kind: Secret
metadata:
  name: keeps-annotations
  namespace: team-dev
  annotations:
    erun.example/owner: expose
type: Opaque
`)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	modified, err := annotatedApplyManifest(document)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	var applied corev1.Secret
	if err := json.Unmarshal(modified, &applied); err != nil {
		t.Fatalf("unmarshal applied: %v", err)
	}
	if applied.Annotations["erun.example/owner"] != "expose" {
		t.Fatalf("the manifest's own annotation was dropped: %v", applied.Annotations)
	}
	if applied.Annotations[lastAppliedConfigurationAnnotation] == "" {
		t.Fatalf("the last-applied annotation was not recorded: %v", applied.Annotations)
	}
}

// The library path is wired only to Secret call sites. A document it cannot
// identify must be refused, never applied on a guess -- and the two shapes the
// other apply call sites use (a multi-document manifest, a custom resource)
// are exactly what would arrive if someone widened the operation without
// widening the implementation.
func TestDecodeSecretManifestRefusesWhatItCannotIdentify(t *testing.T) {
	for name, manifest := range map[string]string{
		"multi_document_resource_quota": "apiVersion: v1\nkind: ResourceQuota\nmetadata:\n  name: erun-quota\n---\napiVersion: v1\nkind: LimitRange\nmetadata:\n  name: erun-limits\n",
		"cert_manager_custom_resource":  "apiVersion: cert-manager.io/v1\nkind: Certificate\nmetadata:\n  name: wildcard\n",
		"ingress":                       "apiVersion: networking.k8s.io/v1\nkind: Ingress\nmetadata:\n  name: app\n",
		"secret_without_a_name":         "apiVersion: v1\nkind: Secret\nmetadata:\n  namespace: team-dev\n",
		"not_yaml":                      "\tthis is not: [a manifest\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := decodeSecretManifest(manifest); err == nil {
				t.Fatalf("expected a refusal, got none")
			}
		})
	}
}

// A first apply has no annotation to merge against, so the patch must carry
// the whole manifest; a re-apply that changes one field must not resend the
// rest. This is the behaviour that distinguishes a three-way merge from a
// wholesale replace, which is what a naive Update would have done.
func TestThreeWaySecretMergePatchSendsOnlyWhatChanged(t *testing.T) {
	document, _, err := decodeSecretManifest(probeSecretManifest)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	modified, err := annotatedApplyManifest(document)
	if err != nil {
		t.Fatalf("annotate: %v", err)
	}
	live := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "team-devops-registry-credential",
			Namespace: "team-dev",
			Labels:    map[string]string{"app.kubernetes.io/managed-by": "erun-init"},
			Annotations: map[string]string{
				lastAppliedConfigurationAnnotation: string(modified),
			},
		},
		Type: corev1.SecretTypeOpaque,
		Data: map[string][]byte{"token": []byte("s3cret")},
	}
	// Re-applying an unchanged manifest against a live object whose recorded
	// original already carries the annotation still rewrites the annotation
	// (it now embeds the previous one), but must not touch the label or type.
	patch, err := threeWaySecretMergePatch(live, modified)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if strings.Contains(string(patch), `"labels"`) {
		t.Fatalf("an unchanged label must not be resent: %s", patch)
	}

	// A label change must appear in the patch.
	changed := strings.Replace(probeSecretManifest, "erun-init", "erun-deploy", 1)
	changedDocument, _, err := decodeSecretManifest(changed)
	if err != nil {
		t.Fatalf("decode changed: %v", err)
	}
	changedModified, err := annotatedApplyManifest(changedDocument)
	if err != nil {
		t.Fatalf("annotate changed: %v", err)
	}
	patch, err = threeWaySecretMergePatch(live, changedModified)
	if err != nil {
		t.Fatalf("patch changed: %v", err)
	}
	if !strings.Contains(string(patch), "erun-deploy") {
		t.Fatalf("a changed label must be sent: %s", patch)
	}
}

// The operation defaults to subprocess like every other one on the switch, and
// doctor must be able to report it -- an operator who flipped it needs
// confirmation it took, not faith.
func TestSecretApplyExecutionModeDefaultsToSubprocessAndIsReported(t *testing.T) {
	if got := ExecutionModeFor(ERunConfig{}, kubectlSecretApplyExecutionOperation); got != ExecutionModeSubprocess {
		t.Fatalf("got %q, want %q", got, ExecutionModeSubprocess)
	}
	config := ERunConfig{Execution: ExecutionConfig{Modes: map[string]string{kubectlSecretApplyExecutionOperation: "library"}}}
	if got := ExecutionModeFor(config, kubectlSecretApplyExecutionOperation); got != ExecutionModeLibrary {
		t.Fatalf("got %q, want %q", got, ExecutionModeLibrary)
	}
	var found bool
	for _, status := range ExecutionModeReport(config) {
		if status.Operation == kubectlSecretApplyExecutionOperation {
			found = true
			if status.Mode != ExecutionModeLibrary {
				t.Fatalf("doctor reports %q, want %q", status.Mode, ExecutionModeLibrary)
			}
		}
	}
	if !found {
		t.Fatalf("doctor does not report %s at all", kubectlSecretApplyExecutionOperation)
	}
}
