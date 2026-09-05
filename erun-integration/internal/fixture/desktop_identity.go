package fixture

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
)

// DesktopIdentity is the desktop's MCP signing identity as a scenario sees it.
type DesktopIdentity struct {
	Dir       string
	KeyPath   string
	PublicKey ed25519.PublicKey
}

// SeedDesktopIdentity writes an Ed25519 desktop identity where erun resolves it,
// so `erun mcp` scenarios can mint bearer tokens for an environment's MCP edge
// without the desktop app ever having run. The public half is returned rather
// than written: the CLI reads only the private key, and a scenario needs the
// public key to verify the token the CLI produced.
func SeedDesktopIdentity(t testing.TB, setup env.Setup) DesktopIdentity {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate desktop identity: %v", err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(private)
	if err != nil {
		t.Fatalf("marshal desktop identity: %v", err)
	}

	dir := desktopIdentityDir(setup)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	keyPath := filepath.Join(dir, "desktopid.key")
	mustWrite(t, keyPath, string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})))
	return DesktopIdentity{Dir: dir, KeyPath: keyPath, PublicKey: public}
}

// SeedDesktopIdentityPublicKey writes the public half of the desktop identity
// where erun resolves it, and returns its path. A deploy that refuses to drop
// live MCP auth looks for it there to tell the operator which key to re-supply,
// so a scenario that exercises that arm needs the file on disk — SeedDesktopIdentity
// deliberately keeps the public half in memory for the token-minting scenarios.
func SeedDesktopIdentityPublicKey(t testing.TB, setup env.Setup, publicKeyPEM string) string {
	t.Helper()
	dir := desktopIdentityDir(setup)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "desktopid.pub")
	mustWrite(t, path, publicKeyPEM)
	return path
}

// The identity lives under os.UserConfigDir()/ERun, which resolves differently
// per host; every branch stays inside the scenario's isolated HOME.
func desktopIdentityDir(setup env.Setup) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(setup.Home, "Library", "Application Support", "ERun")
	}
	// Linux honours XDG_CONFIG_HOME and Windows reads %AppData%; env.Setup points
	// both at the same isolated config directory.
	return filepath.Join(setup.ConfigHome, "ERun")
}
