package kube

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adrg/xdg"
)

// config.yaml holds cluster admin tokens, so the file mode must protect copies outside its 0700 directory.

func setConfigHomeForModeTest(t *testing.T) func() {
	t.Helper()
	previous := xdg.ConfigHome
	xdg.ConfigHome = filepath.Join(t.TempDir(), "config")
	if err := os.MkdirAll(xdg.ConfigHome, 0o700); err != nil {
		t.Fatalf("mkdir config home: %v", err)
	}
	return func() { xdg.ConfigHome = previous }
}
