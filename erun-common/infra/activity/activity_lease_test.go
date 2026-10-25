package activity

import (
	"os"
	"testing"

	"github.com/adrg/xdg"
)

// Injected clocks and liveness: a real dead pid is racy and recyclable.

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

// Resolving the activity directory must not create it (dry-run purity).
func TestEnvironmentActivityDirDoesNotCreateDirectory(t *testing.T) {
	isolateActivityCache(t)
	dir, err := EnvironmentActivityDir("acme", "dev")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if _, statErr := os.Stat(dir); !os.IsNotExist(statErr) {
		t.Fatalf("resolving the activity directory must not create it, got err=%v", statErr)
	}
}
