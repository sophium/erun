package erunmcp

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// Authentication says which tenant is calling; this says what that caller may
// do. The edge needs both, because a token that proves a tenant still reaches
// `raw` — which can kubectl-exec — and every mutating tool besides.
//
// The gate is applied twice on purpose. The registered tool set is filtered per
// request, so `tools/list` shows a caller only what it may call and anything
// else is simply unknown to it; and each surviving handler re-checks at call
// time, so a tool reached by any other route still cannot run. Neither layer
// depends on the SDK propagating anything through context.

// authIdentity is the resolved caller, carried from the auth middleware to the
// per-request server factory and into audit lines.
type authIdentity struct {
	Tenant       string
	User         string
	Capabilities eruncommon.MCPCapabilitySet
}

type authContextKey struct{}

func withAuthIdentity(ctx context.Context, identity authIdentity) context.Context {
	return context.WithValue(ctx, authContextKey{}, identity)
}

// authIdentityFrom returns the resolved caller. An unauthenticated edge — the
// loopback-only deployment that predates a trust anchor — has no identity, and
// resolves to the coarse admin the desktop already assumes, so turning
// authentication off does not quietly turn authorization on.
func authIdentityFrom(ctx context.Context) authIdentity {
	if identity, ok := ctx.Value(authContextKey{}).(authIdentity); ok {
		return identity
	}
	return authIdentity{Capabilities: eruncommon.AdminMCPCapabilitySet()}
}

// toolRegistrar carries what a registration needs to decide. It exists because
// generic functions cannot be methods, so the alternative was threading three
// arguments through every register* call.
type toolRegistrar struct {
	server   *mcp.Server
	identity authIdentity
}

// addTool registers a tool only when the caller may use it. A tool the caller
// cannot call is never registered, so it does not appear in `tools/list` and is
// reported as unknown rather than forbidden — the caller learns nothing about
// what it is not allowed to see.
func addTool[In, Out any](reg toolRegistrar, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	if !reg.identity.Capabilities.AllowsTool(tool.Name) {
		return
	}
	mcp.AddTool(reg.server, tool, guardTool(reg.identity, tool.Name, handler))
}

// guardTool re-checks at call time. Registration already filtered, so this only
// fires if a tool is reached another way — which is exactly when a check that
// depends on registration would have been useless.
func guardTool[In, Out any](identity authIdentity, name string, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		// A server is cached per capability set, so the identity captured at
		// registration is whichever caller first produced that set. Its
		// capabilities are right by construction; its user is not. Prefer the
		// live caller so the audit line names who actually called.
		if live, ok := ctx.Value(authContextKey{}).(authIdentity); ok {
			identity = live
		}
		if !identity.Capabilities.AllowsTool(name) {
			var zero Out
			auditToolDecision(identity, name, false)
			return nil, zero, fmt.Errorf("tool %q requires the %s capability", name, eruncommon.MCPToolCapability(name))
		}
		auditToolDecision(identity, name, true)
		return handler(ctx, req, input)
	}
}

// auditToolDecision records every allow and every deny with the resolved
// identity. A deny nobody can see is indistinguishable from a tool that was
// never called, which is the wrong thing to learn after an incident.
func auditToolDecision(identity authIdentity, tool string, allowed bool) {
	decision := "deny"
	if allowed {
		decision = "allow"
	}
	log.Printf("erun-mcp authz: %s tool=%s tenant=%s user=%s capabilities=%v",
		decision, tool, orUnset(identity.Tenant), orUnset(identity.User), identity.Capabilities.Names())
}

func orUnset(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

// capabilityServerCache keeps one server per distinct capability set. Building a
// server per request would rebuild 40-odd tool registrations on every call, and
// the number of distinct sets is tiny — read, admin, and nothing.
type capabilityServerCache struct {
	mu      sync.Mutex
	servers map[string]*mcp.Server
	build   func(authIdentity) *mcp.Server
}

func newCapabilityServerCache(build func(authIdentity) *mcp.Server) *capabilityServerCache {
	return &capabilityServerCache{servers: map[string]*mcp.Server{}, build: build}
}

func (c *capabilityServerCache) serverFor(req *http.Request) *mcp.Server {
	identity := authIdentityFrom(req.Context())
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
