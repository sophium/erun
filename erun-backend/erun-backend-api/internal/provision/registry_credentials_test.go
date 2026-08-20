package provision

import (
	"context"
	"encoding/base64"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func pullSecret(namespace, name, payload string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Type:       corev1.SecretTypeDockerConfigJson,
		Data:       map[string][]byte{corev1.DockerConfigJsonKey: []byte(payload)},
	}
}

func assertCredential(t *testing.T, credentials *KubeImagePullSecretCredentials, wantUser, wantPassword string) {
	t.Helper()
	credential, ok := credentials.For(context.Background(), "ghcr.io")
	if !ok || credential.Username != wantUser || credential.Password != wantPassword {
		t.Fatalf("For(ghcr.io) = (%+v, %v), want (%s, %s)", credential, ok, wantUser, wantPassword)
	}
}

func TestKubeImagePullSecretCredentials(t *testing.T) {
	auth := base64.StdEncoding.EncodeToString([]byte("encoded-user:encoded-token"))
	kube := fake.NewSimpleClientset(
		pullSecret("erun-prod", "ghcr-pull", `{"auths":{"https://ghcr.io/v1/":{"username":"pull-user","password":"pull-token"}}}`),
		pullSecret("erun-prod", "ghcr-pull-encoded", `{"auths":{"ghcr.io":{"auth":"`+auth+`"}}}`),
		pullSecret("erun-prod", "elsewhere", `{"auths":{"registry.example.com":{"username":"other","password":"other"}}}`),
	)

	t.Run("resolves the host's credential", func(t *testing.T) {
		assertCredential(t, NewKubeImagePullSecretCredentials(kube, "erun-prod", []string{"ghcr-pull"}), "pull-user", "pull-token")
	})

	t.Run("accepts the combined auth field", func(t *testing.T) {
		assertCredential(t, NewKubeImagePullSecretCredentials(kube, "erun-prod", []string{"ghcr-pull-encoded"}), "encoded-user", "encoded-token")
	})

	// An unreadable or irrelevant secret must not end the search: the probe's
	// contract is to stay inconclusive only when nothing can answer.
	t.Run("skips absent and unrelated secrets", func(t *testing.T) {
		credentials := NewKubeImagePullSecretCredentials(kube, "erun-prod", []string{"missing", "elsewhere", "ghcr-pull"})
		assertCredential(t, credentials, "pull-user", "pull-token")
	})

	t.Run("no credential for an unlisted host", func(t *testing.T) {
		credentials := NewKubeImagePullSecretCredentials(kube, "erun-prod", []string{"elsewhere"})
		if _, ok := credentials.For(context.Background(), "ghcr.io"); ok {
			t.Fatal("For(ghcr.io) reported a credential from a secret that names another registry")
		}
	})

	t.Run("unconfigured sources report nothing", func(t *testing.T) {
		for name, credentials := range map[string]*KubeImagePullSecretCredentials{
			"no secrets":   NewKubeImagePullSecretCredentials(kube, "erun-prod", nil),
			"no namespace": NewKubeImagePullSecretCredentials(kube, "  ", []string{"ghcr-pull"}),
			"no client":    NewKubeImagePullSecretCredentials(nil, "erun-prod", []string{"ghcr-pull"}),
		} {
			if _, ok := credentials.For(context.Background(), "ghcr.io"); ok {
				t.Fatalf("%s: For(ghcr.io) reported a credential", name)
			}
		}
	})
}
