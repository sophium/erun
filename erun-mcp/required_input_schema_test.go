package erunmcp

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// toolsExcludedFromRequiredInputAudit mutate real state with no preview mode,
// and this suite has no HOME sandbox; they are not audited.
var toolsExcludedFromRequiredInputAudit = map[string]bool{
	"activity_lease_release":       true,
	"activity_lease_take":          true,
	"cloud_clear_aws_credentials":  true,
	"cloud_inject_aws_credentials": true,
	"idle_stop_cancel":             true,
	"idle_stop_record":             true,
}

// requiredSubjectPattern matches a whole message that is only "<subject> is
// required", not one that merely ends that way after real context.
var requiredSubjectPattern = regexp.MustCompile(`(?i)^([a-z][a-z ]*?) (?:is|are) required$`)

var camelCaseBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// A handler must not require an input its own schema cannot carry.
func TestEveryToolsRequiredInputIsExpressibleInItsOwnSchema(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	t.Setenv("ERUN_CODEX_BIN", "true")
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, model.BuildInfo{Version: "1.2.3"}, runtime)

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools.Tools) == 0 {
		t.Fatal("no tools listed; the rest of this test would pass vacuously")
	}

	for _, tool := range tools.Tools {
		t.Run(tool.Name, func(t *testing.T) {
			auditToolRequiredInputAgainstItsOwnSchema(t, session, tool)
		})
	}
}

func auditToolRequiredInputAgainstItsOwnSchema(t *testing.T, session *mcp.ClientSession, tool *mcp.Tool) {
	t.Helper()
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
}

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

// placeholderFor only satisfies schema validation; domain-level rejections are expected.
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

// schemaType returns the first non-null entry of a type union.
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

// assertErrorNamesOnlyOwnSchemaProperties fails when the error blames a
// property no schema property shares a word with; a shared word means a
// domain-level complaint about the placeholder.
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
	if considered > 0 && orphaned == considered {
		t.Errorf("%s: error says %q is required, but none of that subject's words are a property of this tool's own schema: %s", toolName, strings.TrimSpace(match[1]), message)
	}
}

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

// resultText concatenates the text content, where a handler error's message lands.
func resultText(result *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range result.Content {
		if text, ok := c.(*mcp.TextContent); ok {
			fmt.Fprint(&sb, text.Text)
		}
	}
	return sb.String()
}
