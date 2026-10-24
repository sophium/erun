package callerid

import (
	"context"
	"testing"
)

// No trust anchor means the loopback-only edge, which must keep the admin surface.
func TestAnUnauthenticatedEdgeKeepsTheCoarseAdminSurface(t *testing.T) {
	identity := AuthIdentityFrom(context.Background())
	if !identity.Capabilities.AllowsTool("raw") {
		t.Fatalf("an unauthenticated edge keeps its existing surface, got %+v", identity.Capabilities)
	}
}
