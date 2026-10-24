package release

import (
	"testing"

	"github.com/adrg/xdg"
)

// These cover what the CLI subprocess cannot reach deterministically: expiry and
// the hard lifetime ceiling need an injected clock, and orphan reconciliation
// needs an injected liveness answer (a real dead pid is racy and recyclable).
// The lease commands' own surface is locked by the integration scenarios.

// isolateActivityCache points the activity tree at a temp dir for one test.
func isolateActivityCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}
