package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// The desktop's Ed25519 identity is the trust anchor for every per-env MCP
// bearer: the private key signs tokens and the public half is what a deploy
// injects into the pod. Its location and TTL live here so the desktop, the CLI,
// and any third-party caller mint tokens the deployed edge already trusts — a
// second identity would only produce tokens every environment rejects.
const (
	desktopIdentityDirName = "ERun"
	desktopIdentityKeyFile = "desktopid.key"
	desktopMCPTokenSubject = "erun-desktop"

	// DesktopMCPTokenTTL is kept short so a leaked bearer has a small exploit
	// window; callers mint per request rather than holding one.
	DesktopMCPTokenTTL = 5 * time.Minute
)

// DefaultDesktopIdentityDir returns an empty string when the OS config directory
// cannot be resolved, leaving it to the caller to decide whether that is fatal.
func DefaultDesktopIdentityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, desktopIdentityDirName)
}

func DesktopIdentityKeyPath(dir string) string {
	return filepath.Join(dir, desktopIdentityKeyFile)
}

func DesktopIdentityPublicKeyPath(dir string) string {
	return filepath.Join(dir, desktopMCPPublicKeyFile)
}

// LoadDesktopIdentityKey reads the persisted private key and never generates a
// missing one: a fresh identity signs tokens no deployed environment trusts, so
// the honest outcome is to tell the caller where the identity should come from.
func LoadDesktopIdentityKey(dir string) ([]byte, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, fmt.Errorf("desktop identity directory is unset")
	}
	path := DesktopIdentityKeyPath(dir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("no desktop identity at %s; open an environment from the ERun desktop app once so the identity exists and deployed runtimes trust it", path)
		}
		return nil, fmt.Errorf("read desktop identity %s: %w", path, err)
	}
	return data, nil
}

// SignDesktopMCPToken mints a short-lived bearer for one environment's MCP edge.
// The per-env audience stops a token minted for one environment from being
// replayed against another.
func SignDesktopMCPToken(privatePEM []byte, tenant, environment string, now time.Time) (string, error) {
	return SignMCPToken(privatePEM, MCPTokenClaims{
		Issuer:    DesktopMCPIssuer(),
		Subject:   desktopMCPTokenSubject,
		Audience:  MCPTokenAudience(tenant, environment),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(DesktopMCPTokenTTL).Unix(),
	})
}

// MintDesktopMCPToken loads the desktop identity and signs a fresh token for one
// environment, so a caller holds a bearer only for the request it is about to
// make instead of caching one that can age out mid-call.
func MintDesktopMCPToken(dir, tenant, environment string, now time.Time) (string, error) {
	key, err := LoadDesktopIdentityKey(dir)
	if err != nil {
		return "", err
	}
	return SignDesktopMCPToken(key, tenant, environment, now)
}
