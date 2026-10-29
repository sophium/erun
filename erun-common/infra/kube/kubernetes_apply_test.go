package kube

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

// The annotation is the next merge's original, so its bytes must match kubectl's exactly.
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
	// The annotation is rewritten, but the label and type must not be.
	patch, err := threeWaySecretMergePatch(live, modified)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if strings.Contains(string(patch), `"labels"`) {
		t.Fatalf("an unchanged label must not be resent: %s", patch)
	}

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
