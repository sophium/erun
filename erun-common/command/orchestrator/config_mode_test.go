package orchestrator

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// The config.yaml files hold cluster admin tokens. Their containing directory
// is 0700 today, so the file mode is what still protects them once a copy
// leaves that directory; these tests pin the mode of the files themselves.

// setConfigHomeForModeTest points config resolution at a fresh temp dir and
// returns a restore func.
func setConfigHomeForModeTest(t *testing.T) func() {
	t.Helper()
	previous := xdg.ConfigHome
	xdg.ConfigHome = filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(xdg.ConfigHome, 0o700); err != nil {
		t.Fatalf("mkdir config home: %v", err)
	}
	return func() { xdg.ConfigHome = previous }
}
