package erunmcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// toolsExcludedFromRequiredInputAudit are tools TestEveryToolsRequiredInputIsExpressibleInItsOwnSchema
// does not call: each mutates real, non-sandboxed state (a lease file under
// the real activity cache, injected AWS credentials, a recorded idle-stop
// marker) with no preview mode that would make the call side-effect free.
// erun-mcp's test suite has no HOME-sandboxing harness the way
// erun-integration's env.New gives the CLI suite, so there is no contained
// place for that mutation to land. This is a documented gap, not a silent
// one: if one of these ever grows this same bug (a required input its own
// schema cannot carry), this audit will not catch it.
var toolsExcludedFromRequiredInputAudit = map[string]bool{
	"activity_lease_release":       true,
	"activity_lease_take":          true,
	"cloud_clear_aws_credentials":  true,
	"cloud_inject_aws_credentials": true,
	"idle_stop_cancel":             true,
	"idle_stop_record":             true,
}

// requiredSubjectPattern matches a message that, in its ENTIRETY, is a bare
// "<subject> is/are required" -- the same anchored shape
// erun-integration/bare_required_input_test.go's bareRequiredInputPattern
// checks for tenant/environment specifically, generalized to any subject.
// Anchoring to the whole message (not just a substring anywhere in it) is
// what keeps this from firing on a message that merely ends in that phrase
// after real context, e.g. "a platform block with a base domain is required
// in .erun/config.yaml (...)" -- that names its own operation and location,
// so it is not the dead end this check targets.
var requiredSubjectPattern = regexp.MustCompile(`(?i)^([a-z][a-z ]*?) (?:is|are) required$`)

// camelCaseBoundary finds the position just before a capital letter that
// follows a lowercase one, e.g. in "leaseTtlSeconds" -- used to split a
// schema property name into words for the overlap check below.
var camelCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// TestEveryToolsRequiredInputIsExpressibleInItsOwnSchema is the mechanical
// check for the exec_agent class of bug: a handler that reads a required
// input its own JSON schema has no property for. exec_agent shipped
// uncallable because its handler demanded tenant/environment while its
// schema (additionalProperties: false, no tenant/environment property)
// could not carry them -- no input satisfied it. This drives every tool
// (except the excluded ones above) with only the properties its own schema
// marks required, populated with placeholder values, over a real MCP
// session -- the same schema-validating path a real client uses -- and
// fails if the tool complains about a property its schema does not define,
// or if its error names something "required" that is not one of its own
// schema properties.
func TestEveryToolsRequiredInputIsExpressibleInItsOwnSchema(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	t.Setenv("ERUN_CODEX_BIN", "true")
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, eruncommon.BuildInfo{Version: "1.2.3"}, runtime)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	for _, tool := range tools.Tools {
		tool := tool
		t.Run(tool.Name, func(t *testing.T) {
			if toolsExcludedFromRequiredInputAudit[tool.Name] {
				t.Skip("excluded: mutates real state with no preview mode -- see toolsExcludedFromRequiredInputAudit")
			}
			props, required := schemaPropertiesAndRequired(t, tool)
			readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
			_, hasPreview := props["preview"]
			if !readOnly && !hasPreview {
				t.Skip("excluded: not read-only and has no preview mode to call it side-effect free")
			}

			args := map[string]any{}
			for _, name := range required {
				args[name] = placeholderFor(props[name])
			}
			if hasPreview {
				args["preview"] = true
			}

			result, callErr := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool.Name, Arguments: args})
			if callErr != nil {
				assertErrorNamesOnlyOwnSchemaProperties(t, tool.Name, props, callErr.Error())
				return
			}
			if result.IsError {
				assertErrorNamesOnlyOwnSchemaProperties(t, tool.Name, props, resultText(result))
			}
		})
	}
}

// schemaPropertiesAndRequired reads a tool's wire-shaped InputSchema
// (map[string]any, per mcp.Tool's documented client-side shape) into its
// property definitions and required-property names.
func schemaPropertiesAndRequired(t *testing.T, tool *mcp.Tool) (map[string]any, []string) {
	t.Helper()
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("%s: InputSchema is %T, not map[string]any", tool.Name, tool.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	var required []string
	if raw, ok := schema["required"].([]any); ok {
		for _, r := range raw {
			if name, ok := r.(string); ok {
				required = append(required, name)
			}
		}
	}
	return props, required
}

// placeholderFor synthesizes a minimal value matching a JSON schema
// property's declared type -- just enough to satisfy the SDK's own schema
// validation, not to be meaningful domain input. A domain-level rejection of
// a placeholder (e.g. "review not found") is an expected, healthy outcome;
// this test only cares whether the tool's OWN schema can carry what its
// handler demands.
func placeholderFor(propSchema any) any {
	m, ok := propSchema.(map[string]any)
	if !ok {
		return "x"
	}
	if enum, ok := m["enum"].([]any); ok && len(enum) > 0 {
		return enum[0]
	}
	switch schemaType(m["type"]) {
	case "integer", "number":
		return 1
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return "x"
	}
}

// schemaType normalizes a JSON schema "type" keyword, which may be a bare
// string or a union array (e.g. ["string", "null"]) -- the first non-null
// entry is what a placeholder should match.
func schemaType(raw any) string {
	switch v := raw.(type) {
	case string:
		return v
	case []any:
		for _, entry := range v {
			if s, ok := entry.(string); ok && s != "null" {
				return s
			}
		}
	}
	return ""
}

// assertErrorNamesOnlyOwnSchemaProperties fails when a tool's error, for a
// call carrying only its own required properties, blames a property its
// schema does not define -- either the SDK's own extra-property complaint
// (which should never fire here, since nothing extra was sent) or a bare
// "<subject> is required" whose subject shares no word with any of the
// tool's own schema properties, which is exactly the exec_agent defect: a
// handler-required input the schema has no way to carry. Sharing at least
// one word (e.g. "raw command is required" against a schema property named
// "command") is treated as the schema expressing the subject -- the
// complaint is then a domain-level validation (the placeholder value this
// audit sent was empty/unusable), not a schema-expressiveness gap.
func assertErrorNamesOnlyOwnSchemaProperties(t *testing.T, toolName string, props map[string]any, message string) {
	t.Helper()
	if strings.Contains(strings.ToLower(message), "additional propert") {
		t.Errorf("%s: rejected its own minimal required-only input as carrying extra properties: %s", toolName, message)
	}
	match := requiredSubjectPattern.FindStringSubmatch(strings.TrimSpace(message))
	if match == nil {
		return
	}
	schemaWords := propertyWords(props)
	considered, orphaned := 0, 0
	for _, word := range strings.Fields(strings.ToLower(match[1])) {
		if word == "and" {
			continue
		}
		considered++
		if !schemaWords[word] {
			orphaned++
		}
	}
	// Every word in the subject is unrepresented anywhere in the schema: the
	// schema has no property this error could possibly be about.
	if considered > 0 && orphaned == considered {
		t.Errorf("%s: error says %q is required, but none of that subject's words are a property of this tool's own schema: %s", toolName, strings.TrimSpace(match[1]), message)
	}
}

// propertyWords splits every schema property name into lowercased words on
// camelCase boundaries (e.g. "leaseTtlSeconds" -> "lease", "ttl", "seconds"),
// so a message subject sharing any one of those words is treated as the
// schema representing it.
func propertyWords(props map[string]any) map[string]bool {
	words := map[string]bool{}
	for name := range props {
		split := camelCaseBoundary.ReplaceAllString(name, "$1 $2")
		for _, word := range strings.Fields(strings.ToLower(split)) {
			words[word] = true
		}
	}
	return words
}

// resultText concatenates a CallToolResult's text content, which is where a
// ToolHandlerFor-returned error's message ends up (see mcp.CallToolResult's
// IsError doc).
func resultText(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			fmt.Fprint(&sb, text.Text)
		}
	}
	return sb.String()
}
