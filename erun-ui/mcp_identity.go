package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

const (
	desktopIdentityKeyFile = "desktopid.key"
	desktopIdentityPubFile = "desktopid.pub"
	// desktopMCPTokenTTL keeps the per-env bearer short-lived so a leaked token
	// has a small window; the desktop signs a fresh one per MCP call.
	desktopMCPTokenTTL = 5 * time.Minute
)

// desktopIdentity is the desktop's persistent Ed25519 identity. It
// signs the per-env bearer tokens the desktop sends to each env's MCP edge and
// supplies the public key the deploy injects into the pod so the edge can
// verify them. The private key is the single source of truth, persisted once
// under the user config dir; the public key is written beside it so deploy can
// pass its path to `erun deploy --mcp-auth-public-key`.
type desktopIdentity struct {
	dir        string
	mu         sync.Mutex
	privatePEM []byte
}

// defaultDesktopIdentityDir is the per-user directory the desktop keeps its
// identity in, matching the contribute-state convention (UserConfigDir/ERun).
func defaultDesktopIdentityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, "ERun")
}

func newDesktopIdentity(dir string) *desktopIdentity {
	return &desktopIdentity{dir: dir}
}

func (d *desktopIdentity) keyPath() string       { return filepath.Join(d.dir, desktopIdentityKeyFile) }
func (d *desktopIdentity) publicKeyPath() string { return filepath.Join(d.dir, desktopIdentityPubFile) }

// ensure loads the persisted private key, generating and persisting a fresh
// keypair (and writing the public key beside it) on first use. Safe for
// concurrent callers.
func (d *desktopIdentity) ensure() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.privatePEM) > 0 {
		return nil
	}
	if strings.TrimSpace(d.dir) == "" {
		return fmt.Errorf("desktop identity directory is unset")
	}
	switch data, err := os.ReadFile(d.keyPath()); {
	case err == nil:
		if _, derr := eruncommon.DesktopPublicKeyPEM(data); derr != nil {
			return fmt.Errorf("persisted desktop identity is invalid: %w", derr)
		}
		d.privatePEM = data
	case os.IsNotExist(err):
		priv, _, gerr := eruncommon.GenerateDesktopIdentity()
		if gerr != nil {
			return gerr
		}
		if mkErr := os.MkdirAll(d.dir, 0o700); mkErr != nil {
			return fmt.Errorf("create desktop identity dir: %w", mkErr)
		}
		if wErr := os.WriteFile(d.keyPath(), priv, 0o600); wErr != nil {
			return fmt.Errorf("write desktop identity: %w", wErr)
		}
		d.privatePEM = priv
	default:
		return fmt.Errorf("read desktop identity: %w", err)
	}
	return d.writePublicKeyLocked()
}

// writePublicKeyLocked writes the derived public key beside the private key so
// deploy can reference it by path. The caller holds d.mu.
func (d *desktopIdentity) writePublicKeyLocked() error {
	pub, err := eruncommon.DesktopPublicKeyPEM(d.privatePEM)
	if err != nil {
		return err
	}
	if err := os.WriteFile(d.publicKeyPath(), pub, 0o600); err != nil {
		return fmt.Errorf("write desktop public key: %w", err)
	}
	return nil
}

// ensurePublicKeyPath ensures the identity exists and returns the public-key
// file path for the deploy `--mcp-auth-public-key` input. It returns "" with no
// error when no identity dir is configured, so a deploy from such a desktop
// stays unauthenticated rather than failing.
func (d *desktopIdentity) ensurePublicKeyPath() (string, error) {
	if d == nil || strings.TrimSpace(d.dir) == "" {
		return "", nil
	}
	if err := d.ensure(); err != nil {
		return "", err
	}
	return d.publicKeyPath(), nil
}

// signToken signs a short-lived bearer for the given env's MCP edge, stamped
// with the file:// issuer the edge trusts and the per-env audience it enforces,
// so a token for one env cannot be replayed against another.
func (d *desktopIdentity) signToken(tenant, environment string, now time.Time) (string, error) {
	if d == nil {
		return "", fmt.Errorf("desktop identity is not configured")
	}
	if err := d.ensure(); err != nil {
		return "", err
	}
	d.mu.Lock()
	priv := d.privatePEM
	d.mu.Unlock()
	return eruncommon.SignMCPToken(priv, eruncommon.MCPTokenClaims{
		Issuer:    eruncommon.DesktopMCPIssuer(),
		Subject:   "erun-desktop",
		Audience:  eruncommon.MCPTokenAudience(tenant, environment),
		IssuedAt:  now.Unix(),
		ExpiresAt: now.Add(desktopMCPTokenTTL).Unix(),
	})
}
