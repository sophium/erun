package shared

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"reflect"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/mcptools"
)

// Tools are gated twice: per-request registration hides what a caller cannot
// use, and each handler re-checks at call time.

// AuthIdentity is the resolved caller.
type AuthIdentity struct {
	Tenant       string
	User         string
	Capabilities mcptools.MCPCapabilitySet
}

type authContextKey struct{}

func WithAuthIdentity(ctx context.Context, identity AuthIdentity) context.Context {
	return context.WithValue(ctx, authContextKey{}, identity)
}

// AuthIdentityFrom returns the resolved caller; an unauthenticated edge resolves
// to admin, so disabling authentication does not enable authorization.
func AuthIdentityFrom(ctx context.Context) AuthIdentity {
	if identity, ok := ctx.Value(authContextKey{}).(AuthIdentity); ok {
		return identity
	}
	return AuthIdentity{Capabilities: mcptools.AdminMCPCapabilitySet()}
}

// ToolRegistrar carries the caller context a registration needs.
type ToolRegistrar struct {
	Server   *mcp.Server
	Identity AuthIdentity
	Metrics  *MetricsRecorder
}

// AddTool registers a tool only when the caller may use it, so forbidden tools appear unknown.
func AddTool[In, Out any](reg ToolRegistrar, tool *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) {
	if !reg.Identity.Capabilities.AllowsTool(tool.Name) {
		return
	}
	describeTool(tool)
	AdvertisePreview[In](tool)
	if tool.OutputSchema == nil {
		if schema := outputSchemaFor[Out](); schema != nil {
			tool.OutputSchema = schema
		}
	}
	mcp.AddTool(reg.Server, tool, GuardTool(reg.Identity, tool.Name, reg.Metrics, handler))
}

// PreviewAdvertised is appended when the input accepts `preview`; callers match it verbatim.
const PreviewAdvertised = "Supports preview."

// AdvertisePreview derives the preview sentence from In so the description cannot drift from the schema.
func AdvertisePreview[In any](tool *mcp.Tool) {
	description := strings.TrimSpace(tool.Description)
	claims := strings.HasSuffix(description, PreviewAdvertised)
	accepts := inputAcceptsPreview[In]()
	if !accepts {
		// A description promising a dry run the schema lacks is the mirrored defect.
		if claims {
			panic(fmt.Sprintf("erun-mcp: tool %q advertises preview but its input type has no preview property", tool.Name))
		}
		return
	}
	if claims {
		return
	}
	tool.Description = description + " " + PreviewAdvertised
}

// inputAcceptsPreview reports whether In declares a `preview` property.
func inputAcceptsPreview[In any]() bool {
	return structHasJSONField(reflect.TypeFor[In](), "preview", map[reflect.Type]bool{})
}

func structHasJSONField(rt reflect.Type, name string, seen map[reflect.Type]bool) bool {
	for rt.Kind() == reflect.Pointer {
		rt = rt.Elem()
	}
	if rt.Kind() != reflect.Struct || seen[rt] {
		return false
	}
	seen[rt] = true
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.Anonymous {
			if structHasJSONField(field.Type, name, seen) {
				return true
			}
			continue
		}
		if tag, _, _ := strings.Cut(field.Tag.Get("json"), ","); tag == name {
			return true
		}
	}
	return false
}

// rawJSONSchemaOverrides renders json.RawMessage as any JSON value instead of a byte array.
var rawJSONSchemaOverrides = &jsonschema.ForOptions{
	TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[json.RawMessage](): anyJSONValueSchema(),
	},
}

// anyJSONValueSchema is the six-type union rather than `{}`, which marshals to
// `true`; some clients reject boolean subschemas and refuse the whole tools/list.
func anyJSONValueSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Types: []string{"object", "array", "string", "number", "boolean", "null"},
	}
}

// outputSchemaFor mirrors the SDK's output schema derivation via rawJSONSchemaOverrides; nil for Out == any.
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

// describeTool applies the descriptor table's metadata. It panics on a missing
// descriptor rather than ship the MCP defaults (destructive, open-world).
func describeTool(tool *mcp.Tool) {
	descriptor, ok := mcptools.MCPToolDescriptorFor(tool.Name)
	if !ok {
		panic(fmt.Sprintf("erun-mcp: tool %q has no descriptor in erun-common's MCPToolDescriptor table; add one so it does not ship on the MCP spec defaults", tool.Name))
	}

	tool.Title = descriptor.Title
	// Both are *bool defaulting to true when nil, so set them explicitly.
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
		// No CLI command behind it.
		meta["mcpOnly"] = true
	} else {
		meta["cliPath"] = descriptor.CLIPath
	}
	tool.Meta = meta
}

// GuardTool re-checks capabilities at call time and is the single point counting
// erun_mcp_calls_total and erun_audit_events_total; this edge is MCP-only, so actor_kind is "agent".
func GuardTool[In, Out any](identity AuthIdentity, name string, metrics *MetricsRecorder, handler mcp.ToolHandlerFor[In, Out]) mcp.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcp.CallToolRequest, input In) (*mcp.CallToolResult, Out, error) {
		// Servers are cached per capability set, so audit the live caller. The captured
		// identity is shared across concurrent calls and must stay read-only.
		caller := identity
		if live, ok := ctx.Value(authContextKey{}).(AuthIdentity); ok {
			caller = live
		}
		action := "mcp." + name
		if !caller.Capabilities.AllowsTool(name) {
			var zero Out
			AuditToolDecision(caller, name, false)
			metrics.RecordAuditEvent(action, "agent", "error")
			return nil, zero, fmt.Errorf("tool %q requires the %s capability", name, mcptools.MCPToolCapability(name))
		}
		AuditToolDecision(caller, name, true)
		result, out, err := handler(ctx, req, input)
		label := mcpCallResultLabel(out, err)
		metrics.RecordMCPCall(name, label)
		metrics.RecordAuditEvent(action, "agent", label)
		return result, out, err
	}
}

// AuditToolDecision logs every allow and deny with the resolved identity.
func AuditToolDecision(identity AuthIdentity, tool string, allowed bool) {
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
