package release

import (
	"testing"

	"github.com/adrg/xdg"
)

// These need an injected clock or liveness answer the CLI subprocess cannot give.

func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}
