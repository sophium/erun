package erunmcp

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"sync"

	"github.com/google/jsonschema-go/jsonschema"
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
	metrics  *metricsRecorder
}

// addTool registers a tool only when the caller may use it. A tool the caller
// cannot call is never registered, so it does not appear in `tools/list` and is
// reported as unknown rather than forbidden — the caller learns nothing about
// what it is not allowed to see.
func addTool[In, Out any](reg toolRegistrar, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	if !reg.identity.Capabilities.AllowsTool(tool.Name) {
		return
	}
	describeTool(tool)
	if tool.OutputSchema == nil {
		if schema := outputSchemaFor[Out](); schema != nil {
			tool.OutputSchema = schema
		}
	}
	mcp.AddTool(reg.server, tool, guardTool(reg.identity, tool.Name, reg.metrics, handler))
}

// rawJSONSchemaOverrides widens json.RawMessage fields to accept any JSON
// value. The SDK's reflector otherwise reads json.RawMessage by its
// underlying Go kind — a []byte — and renders the wire representation of "an
// arbitrary JSON value captured verbatim" (e.g. EnvironmentJob.Result) as an
// array of bytes, which the value it actually carries (an object, a string, a
// number...) can never satisfy.
var rawJSONSchemaOverrides = &jsonschema.ForOptions{
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): {},
	},
}

// outputSchemaFor computes the schema for a tool's Out type the same way the
// SDK's own AddTool does when no explicit schema is given, except routed
// through rawJSONSchemaOverrides. It returns nil for Out == any, matching the
// SDK's own choice to omit the output schema entirely in that case.
func outputSchemaFor[Out any]() *jsonschema.Schema {
	rt := reflect.TypeFor[Out]()
	if rt == reflect.TypeFor[any]() {
		return nil
	}
	if rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	schema, err := jsonschema.ForType(rt, rawJSONSchemaOverrides)
	if err != nil {
		panic(fmt.Sprintf("erun-mcp: computing output schema for %s: %v", rt, err))
	}
	return schema
}

// describeTool attaches the tool's family, CLI path, title and annotations from
// erun-common's descriptor table.
//
// It panics on a tool with no descriptor, deliberately. The alternative is
// shipping it on the MCP spec defaults -- readOnlyHint false, destructiveHint
// true, openWorldHint true -- which is how `version` came to be advertised as a
// destructive open-world tool (#1186). A missing descriptor is a programming
// error in a binary that is built and tested together, so failing at
// registration is loud and immediate; a silent conservative default would be
// the same invisible-wrong-answer this table exists to remove.
func describeTool(tool *mcp.Tool) {
	descriptor, ok := eruncommon.MCPToolDescriptorFor(tool.Name)
	if !ok {
		panic(fmt.Sprintf("erun-mcp: tool %q has no descriptor in erun-common's MCPToolDescriptor table; add one so it does not ship on the MCP spec defaults", tool.Name))
	}

	tool.Title = descriptor.Title
	// destructiveHint and openWorldHint are *bool in the SDK and default to
	// TRUE when nil, so both must be set explicitly on every tool -- including
	// to false. That nil-means-true is the whole reason the surface was
	// uniformly destructive before this.
	destructive, openWorld := descriptor.Destructive, descriptor.OpenWorld
	tool.Annotations = &mcp.ToolAnnotations{
		Title:           descriptor.Title,
		ReadOnlyHint:    descriptor.ReadOnly,
		DestructiveHint: &destructive,
		IdempotentHint:  descriptor.Idempotent,
		OpenWorldHint:   &openWorld,
	}

	meta := mcp.Meta{"family": descriptor.Family}
	if descriptor.CLIPath == nil {
		// No command behind it. Reporting mcpOnly is more useful than inventing
		// a path, which is how workspace_sync drifted from `erun sshd sync`.
		meta["mcpOnly"] = true
	} else {
		meta["cliPath"] = descriptor.CLIPath
	}
	tool.Meta = meta
}

// guardTool re-checks at call time. Registration already filtered, so this only
// fires if a tool is reached another way — which is exactly when a check that
// depends on registration would have been useless.
//
// It is also the one place every MCP tool call passes through regardless of
// which register* function added it, which makes it the natural home for
// erun_mcp_calls_total and erun_audit_events_total (erun-docs/docs/
// agent-reference/metrics-spec.md): every call is counted here exactly once,
// with the same allow/deny/outcome guardTool already computes for the log
// line above. actor_kind is always "agent" because this edge is MCP-only — an
// Operator's own actions go through the CLI, a source this in-pod counter does
// not read from yet (see the spec's corrected note on erun_audit_events_total).
func guardTool[In, Out any](identity authIdentity, name string, metrics *metricsRecorder, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		// A server is cached per capability set, so the identity captured at
		// registration is whichever caller first produced that set. Its
		// capabilities are right by construction; its user is not. Prefer the
		// live caller so the audit line names who actually called.
		if live, ok := ctx.Value(authContextKey{}).(authIdentity); ok {
			identity = live
		}
		action := "mcp." + name
		if !identity.Capabilities.AllowsTool(name) {
			var zero Out
			auditToolDecision(identity, name, false)
			metrics.recordAuditEvent(action, "agent", "error")
			return nil, zero, fmt.Errorf("tool %q requires the %s capability", name, eruncommon.MCPToolCapability(name))
		}
		auditToolDecision(identity, name, true)
		result, out, err := handler(ctx, req, input)
		label := mcpCallResultLabel(out, err)
		metrics.recordMCPCall(name, label)
		metrics.recordAuditEvent(action, "agent", label)
		return result, out, err
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
