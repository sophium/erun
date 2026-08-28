package erunmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// TestExecAgentIsCallableWithOnlyAgentAndPrompt is the end-to-end regression
// for the bug where exec_agent's handler demanded a tenant/environment its own
// JSON schema could not carry (additionalProperties: false, no tenant/
// environment property): every call failed, one way or another, no matter
// what the caller sent. This drives a real MCP session over HTTP -- the same
// path a client actually uses, including the SDK's own schema validation --
// rather than calling the Go handler directly, since a direct call bypasses
// exactly the validation layer that rejected the extra properties.
func TestExecAgentIsCallableWithOnlyAgentAndPrompt(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	runtime := RuntimeConfig{Context: RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, eruncommon.BuildInfo{Version: "1.2.3"}, runtime)

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "exec_agent",
		Arguments: map[string]any{"agent": "claude", "prompt": "probe", "preview": true},
	})
	if err != nil {
		t.Fatalf("exec_agent rejected a call carrying only its own required+documented properties: %v", err)
	}
	if result.IsError {
		t.Fatalf("exec_agent returned a tool-level error for its own minimal input: %v", result.Content)
	}

	encoded, marshalErr := json.MarshalIndent(result.StructuredContent, "", "  ")
	if marshalErr != nil {
		t.Fatalf("marshal exec_agent's response: %v", marshalErr)
	}
	t.Logf("exec_agent({agent:claude, prompt:probe, preview:true}) resolved:\n%s", encoded)
}
