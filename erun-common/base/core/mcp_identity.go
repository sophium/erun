package core

import (
	"os"
	"path/filepath"
	"time"
)

// The desktop's Ed25519 identity signs every per-env MCP bearer; a deploy
// injects its public half into the pod, so all callers must share it.
const (
	desktopIdentityDirName = "ERun"
	DesktopIdentityKeyFile = "desktopid.key"
	DesktopMCPTokenSubject = "erun-desktop"

	// DesktopMCPTokenTTL is short to limit a leaked bearer; mint per request.
	DesktopMCPTokenTTL = 5 * time.Minute
)

// DefaultDesktopIdentityDir returns "" when the OS config dir is unresolvable.
func DefaultDesktopIdentityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(configDir, desktopIdentityDirName)
}
