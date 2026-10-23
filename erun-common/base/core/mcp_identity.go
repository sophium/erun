package core

import (
	"os"
	"path/filepath"
	"time"
)

// The desktop's Ed25519 identity is the trust anchor for every per-env MCP
// bearer: the private key signs tokens and the public half is what a deploy
// injects into the pod. Its location and TTL live here so the desktop, the CLI,
// and any third-party caller mint tokens the deployed edge already trusts — a
// second identity would only produce tokens every environment rejects.
const (
	desktopIdentityDirName = "ERun"
	DesktopIdentityKeyFile = "desktopid.key"
	DesktopMCPTokenSubject = "erun-desktop"

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
