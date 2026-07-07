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
	dockerConfigDir             = defaultDockerConfigDir
	dockerCredentialHelperPaths = defaultDockerCredentialHelperPaths
	runDockerCredentialHelper   = execDockerCredentialHelper
	resolveGHCRTokenViaGH       = ghAuthToken
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
// lists it"), then a gh-issued token, then an explicit GH_TOKEN/GITHUB_TOKEN.
// The env-var path is last but load-bearing: it needs no credential-helper or gh
// subprocess, so it stays reachable when a desktop app's subprocess or keychain
// access is intermittently blocked (endpoint security, a locked keychain) — the
// failure that otherwise strands the picker on anonymous access. owner scopes the
// credential to the account that owns the namespace.
func resolveGHCRBasicAuth(owner string) (registryBasicAuth, bool) {
	if auth, ok := resolveRegistryBasicAuth("ghcr.io"); ok {
		return auth, true
	}
	if token, ok := resolveGHCRTokenViaGH(owner); ok {
		return ghcrTokenBasicAuth(owner, token), true
	}
	if token, ok := ghcrTokenFromEnv(); ok {
		return ghcrTokenBasicAuth(owner, token), true
	}
	return registryBasicAuth{}, false
}

// ghcrTokenEnvVars are the token env vars honored for ghcr.io, in gh's own
// precedence order, so an operator's existing GitHub token authenticates the
// picker with no keychain or subprocess dependency.
var ghcrTokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

func ghcrTokenFromEnv() (string, bool) {
	for _, name := range ghcrTokenEnvVars {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, true
		}
	}
	return "", false
}

// ghcrTokenBasicAuth pairs a bearer token with the namespace owner as the basic-
// auth username; GHCR ignores the username on a token exchange but requires one,
// so an empty owner uses the conventional token placeholder.
func ghcrTokenBasicAuth(owner, token string) registryBasicAuth {
	user := strings.TrimSpace(owner)
	if user == "" {
		user = "x-access-token"
	}
	return registryBasicAuth{username: user, secret: token}
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

// credentialFromHelper tries every docker-credential-<helper> binary it can find
// until one returns a credential. Multiple container runtimes (Rancher Desktop,
// OrbStack, Docker Desktop) each ship a helper of the same name, but only the one
// holding the login keychain item returns a secret — the others return empty — so
// stopping at the first found binary can miss the working credential.
func credentialFromHelper(helper, host string) (registryBasicAuth, bool) {
	for _, bin := range dockerCredentialHelperPaths(helper) {
		for _, server := range hostLookupKeys(host) {
			out, err := runDockerCredentialHelper(bin, server)
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

func execDockerCredentialHelper(binaryPath, serverURL string) ([]byte, error) {
	cmd := Command(binaryPath, "get")
	cmd.Stdin = strings.NewReader(serverURL)
	return cmd.Output()
}

// defaultDockerCredentialHelperPaths lists docker-credential-<helper> binaries to
// try, in priority order: the one on PATH first, then well-known install dirs. The
// dir search is what lets a macOS GUI app launched from Finder/Dock — which starts
// with a stripped PATH, or a login-shell PATH that resolves the wrong runtime's
// helper — still reach the helper binary that actually holds the credential.
func defaultDockerCredentialHelperPaths(helper string) []string {
	bin := "docker-credential-" + helper
	seen := make(map[string]struct{}, 4)
	paths := make([]string, 0, 4)
	add := func(p string) {
		if p == "" {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	if p, err := exec.LookPath(bin); err == nil {
		add(p)
	}
	for _, dir := range dockerToolDirs() {
		candidate := filepath.Join(dir, bin)
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			add(candidate)
		}
	}
	if len(paths) == 0 {
		add(bin) // bare name; Command re-searches PATH or fails cleanly
	}
	return paths
}

func dockerToolDirs() []string {
	dirs := []string{"/usr/local/bin", "/opt/homebrew/bin"}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs,
			filepath.Join(home, ".rd", "bin"),
			filepath.Join(home, ".orbstack", "bin"),
			filepath.Join(home, ".docker", "bin"),
		)
	}
	return append(dirs, "/Applications/Docker.app/Contents/Resources/bin", "/opt/podman/bin")
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
