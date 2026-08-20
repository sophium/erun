package provision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// RegistryCredential is one registry host's pull identity.
type RegistryCredential struct {
	Username string
	Password string
}

// Usable reports whether the credential can be presented at all; a half-filled
// entry in a docker config is treated as no credential rather than sent.
func (c RegistryCredential) Usable() bool {
	return c.Username != "" && c.Password != ""
}

// RegistryCredentials resolves the credential a registry host is pulled with,
// so a probe can ask the registry the same question the deploy Job's kubelet
// gets to ask.
type RegistryCredentials interface {
	// For returns host's credential, or ok=false when none is available, which
	// leaves the probe unauthenticated and therefore unable to distinguish an
	// absent repository from a private one.
	For(ctx context.Context, host string) (RegistryCredential, bool)
}

// KubeImagePullSecretCredentials reads the dockerconfigjson Secrets the deploy
// Job's ServiceAccount pulls with, in the platform namespace the API itself
// runs in. Reading them at probe time rather than at startup means a rotated
// pull secret is picked up without restarting the API.
type KubeImagePullSecretCredentials struct {
	kube      kubernetes.Interface
	namespace string
	names     []string
}

func NewKubeImagePullSecretCredentials(kube kubernetes.Interface, namespace string, names []string) *KubeImagePullSecretCredentials {
	return &KubeImagePullSecretCredentials{kube: kube, namespace: strings.TrimSpace(namespace), names: names}
}

// For returns the first credential any configured secret carries for host. An
// unreadable secret (absent, or no RBAC to get it) is skipped rather than
// fatal: the caller's contract is to stay inconclusive when it cannot tell.
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
	return RegistryCredential{}, false
}

// dockerConfigCredential pulls host's entry out of a `.dockerconfigjson`
// payload. Either the split username/password fields or the combined base64
// `auth` field is accepted, because both are what a registry login writes.
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

// registryHostMatches compares a docker config's registry key against a bare
// host. A key may carry a scheme and a path (`https://ghcr.io/v1/`), both of
// which docker itself ignores when matching.
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
