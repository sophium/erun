package records

import (
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
