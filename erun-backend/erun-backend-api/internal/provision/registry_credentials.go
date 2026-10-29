package provision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"log"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RegistryCredential is one registry host's pull identity.
type RegistryCredential struct {
	Username string
	Password string
}

// Usable reports whether the credential is complete; a half-filled one is not sent.
func (c RegistryCredential) Usable() bool {
	return c.Username != "" && c.Password != ""
}

// RegistryCredentials resolves the credential a registry host is pulled with.
type RegistryCredentials interface {
	// For returns ok=false when no credential is available, leaving the probe
	// unable to tell an absent repository from a private one.
	For(ctx context.Context, host string) (RegistryCredential, bool)
}

// KubeImagePullSecretCredentials reads the deploy Job's pull secrets at probe
// time, so a rotated secret is picked up without a restart.
type KubeImagePullSecretCredentials struct {
	kube      kubernetes.Interface
	namespace string
	names     []string
	warnOnce  sync.Once
}

func NewKubeImagePullSecretCredentials(kube kubernetes.Interface, namespace string, names []string) *KubeImagePullSecretCredentials {
	return &KubeImagePullSecretCredentials{kube: kube, namespace: strings.TrimSpace(namespace), names: names}
}

// For skips unreadable secrets: the probe stays inconclusive rather than failing.
func (c *KubeImagePullSecretCredentials) For(ctx context.Context, host string) (RegistryCredential, bool) {
	if c == nil || c.kube == nil || c.namespace == "" {
		return RegistryCredential{}, false
	}
	for _, name := range c.names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		secret, err := c.kube.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			continue
		}
		if credential, ok := dockerConfigCredential(secret.Data[corev1.DockerConfigJsonKey], host); ok {
			return credential, true
		}
	}
	c.warnCredentialMiss(host)
	return RegistryCredential{}, false
}

// warnCredentialMiss logs once per host so an unreadable or misnamed secret is
// distinguishable from the deliberate "no secrets configured" state.
func (c *KubeImagePullSecretCredentials) warnCredentialMiss(host string) {
	if len(c.names) == 0 {
		return
	}
	c.warnOnce.Do(func() {
		log.Printf("erun api registry credentials: no usable pull credential for host %q among secrets %v in namespace %q -- the runtime image probe stays unauthenticated and will not confirm a missing tenant image; confirm the ServiceAccount can get these secrets", host, c.names, c.namespace)
	})
}

// dockerConfigCredential accepts either username/password or the base64 `auth` field.
func dockerConfigCredential(payload []byte, host string) (RegistryCredential, bool) {
	if len(payload) == 0 {
		return RegistryCredential{}, false
	}
	var config struct {
		Auths map[string]struct {
			Username string `json:"username"`
			Password string `json:"password"`
			Auth     string `json:"auth"`
		} `json:"auths"`
	}
	if err := json.Unmarshal(payload, &config); err != nil {
		return RegistryCredential{}, false
	}
	for registry, entry := range config.Auths {
		if !registryHostMatches(registry, host) {
			continue
		}
		credential := RegistryCredential{Username: entry.Username, Password: entry.Password}
		if !credential.Usable() {
			credential = decodeDockerAuth(entry.Auth)
		}
		if credential.Usable() {
			return credential, true
		}
	}
	return RegistryCredential{}, false
}

// registryHostMatches ignores scheme and path in the key, as docker does.
func registryHostMatches(registry, host string) bool {
	registry = strings.TrimSpace(registry)
	if scheme := strings.Index(registry, "://"); scheme >= 0 {
		registry = registry[scheme+3:]
	}
	registry, _, _ = strings.Cut(registry, "/")
	return strings.EqualFold(registry, strings.TrimSpace(host))
}

func decodeDockerAuth(auth string) RegistryCredential {
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(auth))
	if err != nil {
		return RegistryCredential{}
	}
	username, password, found := strings.Cut(string(decoded), ":")
	if !found {
		return RegistryCredential{}
	}
	return RegistryCredential{Username: username, Password: password}
}
