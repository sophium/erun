package eruncommon

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// registryBasicAuth is a username/secret pair used to Basic-authenticate a
// registry token request, so version listing can read private images the
// operator is already logged into rather than falling back to anonymous access.
type registryBasicAuth struct {
	username string
	secret   string
}

// Seams so tests exercise the resolution logic without a real docker config, a
// live credential keychain, or the gh CLI. Mirrors the aws_sso_config.go style.
var (
	dockerConfigDir           = defaultDockerConfigDir
	runDockerCredentialHelper = execDockerCredentialHelper
	resolveGHCRTokenViaGH     = ghAuthToken
)

type dockerConfig struct {
	Auths       map[string]dockerAuthEntry `json:"auths"`
	CredsStore  string                     `json:"credsStore"`
	CredHelpers map[string]string          `json:"credHelpers"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// resolveGHCRBasicAuth resolves a pull credential for ghcr.io, preferring the
// credential docker itself pulls with (so "if docker can pull it, the picker
// lists it") and falling back to a gh-issued token. owner scopes the gh token
// to the account that owns the namespace.
func resolveGHCRBasicAuth(owner string) (registryBasicAuth, bool) {
	if auth, ok := resolveRegistryBasicAuth("ghcr.io"); ok {
		return auth, true
	}
	token, ok := resolveGHCRTokenViaGH(owner)
	if !ok {
		return registryBasicAuth{}, false
	}
	user := strings.TrimSpace(owner)
	if user == "" {
		user = "x-access-token"
	}
	return registryBasicAuth{username: user, secret: token}, true
}

// resolveRegistryBasicAuth looks up a host's credential the way docker does: a
// per-host credential helper (or the global creds store) first, then an inline
// base64 auth entry. Returns false when no usable credential is configured.
func resolveRegistryBasicAuth(host string) (registryBasicAuth, bool) {
	cfg, ok := loadDockerConfig()
	if !ok {
		return registryBasicAuth{}, false
	}
	if helper := dockerCredentialHelperFor(cfg, host); helper != "" {
		if auth, ok := credentialFromHelper(helper, host); ok {
			return auth, true
		}
	}
	return credentialFromInlineAuths(cfg, host)
}

func loadDockerConfig() (dockerConfig, bool) {
	dir := dockerConfigDir()
	if strings.TrimSpace(dir) == "" {
		return dockerConfig{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return dockerConfig{}, false
	}
	var cfg dockerConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return dockerConfig{}, false
	}
	return cfg, true
}

func defaultDockerConfigDir() string {
	if dir := strings.TrimSpace(os.Getenv("DOCKER_CONFIG")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		return ""
	}
	return filepath.Join(home, ".docker")
}

// dockerCredentialHelperFor picks the credential helper for a host: a per-host
// credHelpers entry overrides the global credsStore.
func dockerCredentialHelperFor(cfg dockerConfig, host string) string {
	for _, key := range hostLookupKeys(host) {
		if helper := strings.TrimSpace(cfg.CredHelpers[key]); helper != "" {
			return helper
		}
	}
	return strings.TrimSpace(cfg.CredsStore)
}

func credentialFromHelper(helper, host string) (registryBasicAuth, bool) {
	for _, server := range hostLookupKeys(host) {
		out, err := runDockerCredentialHelper(helper, server)
		if err != nil {
			continue
		}
		var creds struct {
			Username string `json:"Username"`
			Secret   string `json:"Secret"`
		}
		if err := json.Unmarshal(out, &creds); err != nil {
			continue
		}
		if strings.TrimSpace(creds.Secret) == "" {
			continue
		}
		return registryBasicAuth{username: strings.TrimSpace(creds.Username), secret: strings.TrimSpace(creds.Secret)}, true
	}
	return registryBasicAuth{}, false
}

func credentialFromInlineAuths(cfg dockerConfig, host string) (registryBasicAuth, bool) {
	for _, key := range hostLookupKeys(host) {
		entry, ok := cfg.Auths[key]
		if !ok {
			continue
		}
		if auth, ok := decodeInlineDockerAuth(entry.Auth); ok {
			return auth, true
		}
	}
	return registryBasicAuth{}, false
}

func decodeInlineDockerAuth(encoded string) (registryBasicAuth, bool) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return registryBasicAuth{}, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return registryBasicAuth{}, false
	}
	user, secret, ok := strings.Cut(string(decoded), ":")
	if !ok || strings.TrimSpace(secret) == "" {
		return registryBasicAuth{}, false
	}
	return registryBasicAuth{username: strings.TrimSpace(user), secret: secret}, true
}

// hostLookupKeys returns the config/keychain keys docker may have stored a
// host's credential under, most specific first. Docker Hub is keyed by its
// legacy v1 index URL.
func hostLookupKeys(host string) []string {
	host = strings.TrimSpace(host)
	keys := []string{host, "https://" + host, "https://" + host + "/"}
	if host == "index.docker.io" || host == "registry-1.docker.io" || host == "docker.io" {
		keys = append(keys, "https://index.docker.io/v1/")
	}
	return keys
}

func execDockerCredentialHelper(helper, serverURL string) ([]byte, error) {
	cmd := Command("docker-credential-"+helper, "get")
	cmd.Stdin = strings.NewReader(serverURL)
	return cmd.Output()
}

func ghAuthToken(owner string) (string, bool) {
	if _, err := exec.LookPath("gh"); err != nil {
		return "", false
	}
	args := []string{"auth", "token", "-h", "github.com"}
	if owner = strings.TrimSpace(owner); owner != "" {
		args = append(args, "-u", owner)
	}
	token, err := captureGHCommand(args...)
	if err != nil {
		return "", false
	}
	if token = strings.TrimSpace(token); token != "" {
		return token, true
	}
	return "", false
}
