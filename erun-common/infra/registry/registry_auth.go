package registry

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// RegistryBasicAuth lets version listing read private images the operator is logged into.
type RegistryBasicAuth struct {
	Username string
	Secret   string
}

// Test seams.
var (
	DockerConfigDir             = defaultDockerConfigDir
	dockerCredentialHelperPaths = defaultDockerCredentialHelperPaths
	runDockerCredentialHelper   = execDockerCredentialHelper
	RunECRLoginPassword         = execECRLoginPassword
	ResolveGHCRTokenViaGH       = ghAuthToken
)

type dockerConfig struct {
	Auths       map[string]dockerAuthEntry `json:"auths"`
	CredsStore  string                     `json:"credsStore"`
	CredHelpers map[string]string          `json:"credHelpers"`
}

type dockerAuthEntry struct {
	Auth string `json:"auth"`
}

// resolveGHCRBasicAuth tries docker's own credential, gh, GH_TOKEN/GITHUB_TOKEN,
// then the in-pod declared Secret. The env-var route needs no subprocess or
// keychain, so it survives when those are blocked.
func resolveGHCRBasicAuth(owner string) (RegistryBasicAuth, bool) {
	if auth, ok := resolveRegistryBasicAuth("ghcr.io"); ok {
		return auth, true
	}
	if token, ok := ResolveGHCRTokenViaGH(owner); ok {
		return ghcrTokenBasicAuth(owner, token), true
	}
	if token, ok := GhcrTokenFromEnv(); ok {
		return ghcrTokenBasicAuth(owner, token), true
	}
	if auth, ok := inPodDeclaredRegistryAuthFor("ghcr.io"); ok {
		return auth, true
	}
	return RegistryBasicAuth{}, false
}

// ghcrTokenEnvVars are in gh's own precedence order.
var ghcrTokenEnvVars = []string{"GH_TOKEN", "GITHUB_TOKEN"}

func GhcrTokenFromEnv() (string, bool) {
	for _, name := range ghcrTokenEnvVars {
		if token := strings.TrimSpace(os.Getenv(name)); token != "" {
			return token, true
		}
	}
	return "", false
}

// ghcrTokenBasicAuth uses the owner as username; GHCR ignores it but requires one.
func ghcrTokenBasicAuth(owner, token string) RegistryBasicAuth {
	user := strings.TrimSpace(owner)
	if user == "" {
		user = "x-access-token"
	}
	return RegistryBasicAuth{Username: user, Secret: token}
}

// resolveRegistryBasicAuth looks up a credential the way docker does.
func resolveRegistryBasicAuth(host string) (RegistryBasicAuth, bool) {
	cfg, ok := loadDockerConfig()
	if !ok {
		return RegistryBasicAuth{}, false
	}
	if helper := dockerCredentialHelperFor(cfg, host); helper != "" {
		if auth, ok := credentialFromHelper(helper, host); ok {
			return auth, true
		}
	}
	return credentialFromInlineAuths(cfg, host)
}

func loadDockerConfig() (dockerConfig, bool) {
	dir := DockerConfigDir()
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

func dockerCredentialHelperFor(cfg dockerConfig, host string) string {
	for _, key := range hostLookupKeys(host) {
		if helper := strings.TrimSpace(cfg.CredHelpers[key]); helper != "" {
			return helper
		}
	}
	return strings.TrimSpace(cfg.CredsStore)
}

// credentialFromHelper tries every same-named helper binary: several runtimes
// ship one, but only the one holding the keychain item returns a secret.
func credentialFromHelper(helper, host string) (RegistryBasicAuth, bool) {
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
			return RegistryBasicAuth{Username: strings.TrimSpace(creds.Username), Secret: strings.TrimSpace(creds.Secret)}, true
		}
	}
	return RegistryBasicAuth{}, false
}

func credentialFromInlineAuths(cfg dockerConfig, host string) (RegistryBasicAuth, bool) {
	for _, key := range hostLookupKeys(host) {
		entry, ok := cfg.Auths[key]
		if !ok {
			continue
		}
		if auth, ok := decodeInlineDockerAuth(entry.Auth); ok {
			return auth, true
		}
	}
	return RegistryBasicAuth{}, false
}

func decodeInlineDockerAuth(encoded string) (RegistryBasicAuth, bool) {
	encoded = strings.TrimSpace(encoded)
	if encoded == "" {
		return RegistryBasicAuth{}, false
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return RegistryBasicAuth{}, false
	}
	user, secret, ok := strings.Cut(string(decoded), ":")
	if !ok || strings.TrimSpace(secret) == "" {
		return RegistryBasicAuth{}, false
	}
	return RegistryBasicAuth{Username: strings.TrimSpace(user), Secret: secret}, true
}

// hostLookupKeys returns docker's possible keys for host; Docker Hub uses the legacy v1 URL.
func hostLookupKeys(host string) []string {
	host = strings.TrimSpace(host)
	keys := []string{host, "https://" + host, "https://" + host + "/"}
	if host == "index.docker.io" || host == "registry-1.docker.io" || host == "docker.io" {
		keys = append(keys, "https://index.docker.io/v1/")
	}
	return keys
}

func execDockerCredentialHelper(binaryPath, serverURL string) ([]byte, error) {
	cmd := core.Command(binaryPath, "get")
	cmd.Stdin = strings.NewReader(serverURL)
	return cmd.Output()
}

// defaultDockerCredentialHelperPaths also searches well-known dirs, because a
// macOS GUI app starts with a stripped or wrong PATH.
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
	token, err := CaptureGHCommand(args...)
	if err != nil {
		return "", false
	}
	if token = strings.TrimSpace(token); token != "" {
		return token, true
	}
	return "", false
}

func ecrRegionFromHost(host string) (string, bool) {
	host = strings.ToLower(strings.TrimSpace(host))
	if !strings.HasSuffix(host, ".amazonaws.com") {
		return "", false
	}
	parts := strings.Split(host, ".")
	for i, part := range parts {
		if part == "ecr" && i+1 < len(parts) {
			if region := strings.TrimSpace(parts[i+1]); region != "" {
				return region, true
			}
		}
	}
	return "", false
}

// ResolveOCIRegistryBasicAuth resolves a pull credential for any registry host.
func ResolveOCIRegistryBasicAuth(host string) (RegistryBasicAuth, bool) {
	// ECR first: stored ECR tokens expire after 12 hours and an expired one would
	// shadow the fallback.
	if region, ok := ecrRegionFromHost(host); ok {
		if token, ok := RunECRLoginPassword(region); ok {
			return RegistryBasicAuth{Username: "AWS", Secret: token}, true
		}
	}
	if auth, ok := resolveRegistryBasicAuth(host); ok {
		return auth, true
	}
	return inPodDeclaredRegistryAuthFor(host)
}

func execECRLoginPassword(region string) (string, bool) {
	cmd := exec.Command("aws", "ecr", "get-login-password", "--region", region)
	out, err := cmd.Output()
	if err != nil {
		return "", false
	}
	token := strings.TrimSpace(string(out))
	if token == "" {
		return "", false
	}
	return token, true
}
