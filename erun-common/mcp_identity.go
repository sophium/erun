package eruncommon

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
)

func DesktopIdentityKeyPath(dir string) string {
	return filepath.Join(dir, core.DesktopIdentityKeyFile)
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
		Subject:   core.DesktopMCPTokenSubject,
		Audience:  MCPTokenAudience(tenant, environment),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(core.DesktopMCPTokenTTL).Unix(),
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
