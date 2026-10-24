package erunmcp

import (
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-mcp/tools/shared/callerid"
)

// capabilityServerCache keeps one server per capability set, avoiding re-registering
// every tool per request.
type capabilityServerCache struct {
	mu      sync.Mutex
	servers map[string]*mcp.Server
	build   func(callerid.AuthIdentity) *mcp.Server
}

func newCapabilityServerCache(build func(callerid.AuthIdentity) *mcp.Server) *capabilityServerCache {
	return &capabilityServerCache{servers: map[string]*mcp.Server{}, build: build}
}

func (c *capabilityServerCache) serverFor(req *http.Request) *mcp.Server {
	identity := callerid.AuthIdentityFrom(req.Context())
	// The tenant is fixed per edge, so the capability set alone keys the surface.
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
