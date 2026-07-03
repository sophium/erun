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
	// Kept short-lived so a leaked bearer has a small exploit window.
	desktopMCPTokenTTL = 5 * time.Minute
)

// desktopIdentity is the desktop's persistent Ed25519 identity: it signs the
// per-env MCP bearer tokens each env's edge verifies, and supplies the public
// key the deploy injects into the pod so that edge can trust them.
type desktopIdentity struct {
	dir        string
	mu         sync.Mutex
	privatePEM []byte
}

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

// Returns "" with no error when no identity dir is configured, so a deploy from
// such a desktop stays unauthenticated rather than failing.
func (d *desktopIdentity) ensurePublicKeyPath() (string, error) {
	if d == nil || strings.TrimSpace(d.dir) == "" {
		return "", nil
	}
	if err := d.ensure(); err != nil {
		return "", err
	}
	return d.publicKeyPath(), nil
}

// The per-env audience prevents a token minted for one env from being replayed
// against another.
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
