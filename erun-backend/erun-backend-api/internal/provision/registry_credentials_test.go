package provision

import (
	"bytes"
	"context"
	"encoding/base64"
	"log"
	"strings"
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

// captureLog redirects the standard logger for the duration of fn and returns
// what it wrote, so a test can assert on the one place an operator would see
// why the registry probe stayed unauthenticated.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	original := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(original)
	fn()
	return buf.String()
}

func TestKubeImagePullSecretCredentialsLogsAnUnreadableSecretOnce(t *testing.T) {
	// missing-role stands in for #1058: the Secret exists but the
	// ServiceAccount's Role does not grant "get" on it, so the fake client
	// (which has no RBAC of its own) is instead given no such secret at all --
	// the same "cannot read it" outcome the checker sees either way.
	kube := fake.NewSimpleClientset()
	credentials := NewKubeImagePullSecretCredentials(kube, "erun-prod", []string{"missing-role"})

	output := captureLog(t, func() {
		for i := 0; i < 3; i++ {
			if _, ok := credentials.For(context.Background(), "ghcr.io"); ok {
				t.Fatal("For(ghcr.io) unexpectedly reported a credential")
			}
		}
	})

	if strings.Count(output, "missing-role") != 1 {
		t.Fatalf("expected exactly one log line naming the secret it looked for, got: %q", output)
	}
	if !strings.Contains(output, "erun-prod") || !strings.Contains(output, "ghcr.io") {
		t.Fatalf("expected the log line to name the namespace and host, got: %q", output)
	}
}

func TestKubeImagePullSecretCredentialsDoesNotLogWhenNoSecretsAreConfigured(t *testing.T) {
	// Empty is the deliberate "no probe configured" state (see
	// EnvDeployConfig.ImagePullSecrets) -- not a misconfiguration, so it must
	// stay silent.
	kube := fake.NewSimpleClientset()
	credentials := NewKubeImagePullSecretCredentials(kube, "erun-prod", nil)

	output := captureLog(t, func() {
		if _, ok := credentials.For(context.Background(), "ghcr.io"); ok {
			t.Fatal("For(ghcr.io) unexpectedly reported a credential")
		}
	})

	if output != "" {
		t.Fatalf("expected no log output when no pull secrets are configured, got: %q", output)
	}
}
