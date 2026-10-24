package jobs

import (
	"testing"

	"github.com/adrg/xdg"
)

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}

func alwaysAlive(int) bool { return true }
func neverAlive(int) bool  { return false }
