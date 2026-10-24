package aisessions

import (
	"testing"

	"github.com/adrg/xdg"
)

// The lease tools are how an orchestrator driving detached work in this pod
// tells the environment what it is doing. What matters here is the transport
// contract: the server's own tenant/environment context is used when the caller
// omits it, and every call reports the whole claim set rather than only the
// lease it just moved.

func isolateLeaseCache(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	xdg.Reload()
	t.Cleanup(xdg.Reload)
}
