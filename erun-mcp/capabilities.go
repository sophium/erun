package erunmcp

import (
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// capabilityServerCache keeps one server per distinct capability set. Building a
// server per request would rebuild 40-odd tool registrations on every call, and
// the number of distinct sets is tiny — read, admin, and nothing.
type capabilityServerCache struct {
	mu      sync.Mutex
	servers map[string]*mcp.Server
	build   func(shared.AuthIdentity) *mcp.Server
}

func newCapabilityServerCache(build func(shared.AuthIdentity) *mcp.Server) *capabilityServerCache {
	return &capabilityServerCache{servers: map[string]*mcp.Server{}, build: build}
}

func (c *capabilityServerCache) serverFor(req *http.Request) *mcp.Server {
	identity := shared.AuthIdentityFrom(req.Context())
	// The tenant is already fixed per edge, so the capability set alone
	// identifies a distinct tool surface.
	key := identity.Capabilities.Key()
	c.mu.Lock()
	defer c.mu.Unlock()
	if server, ok := c.servers[key]; ok {
		return server
	}
	server := c.build(identity)
	c.servers[key] = server
	return server
}
