package orchestrator

import (
	"testing"

	"github.com/adrg/xdg"
)

// Expiry, the lifetime ceiling and orphan reconciliation need an injected clock or liveness.

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}
