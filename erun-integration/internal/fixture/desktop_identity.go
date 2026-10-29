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

// SeedDesktopIdentity writes the Ed25519 private key where erun resolves it and returns the public half.
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

// SeedDesktopIdentityPublicKey writes the public half to disk, for the deploy arm that names the key to re-supply.
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

// desktopIdentityDir mirrors os.UserConfigDir()/ERun inside the isolated HOME.
func desktopIdentityDir(setup env.Setup) string {
	if runtime.GOOS == "darwin" {
		return filepath.Join(setup.Home, "Library", "Application Support", "ERun")
	}
	// env.Setup points XDG_CONFIG_HOME and %AppData% at the same directory.
	return filepath.Join(setup.ConfigHome, "ERun")
}
