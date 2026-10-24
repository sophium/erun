package erunmcp

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// Drives a real MCP session so the SDK's schema validation runs, which a direct handler
// call would bypass.
func TestExecAgentIsCallableWithOnlyAgentAndPrompt(t *testing.T) {
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	// Preview never execs the supervisor, so a placeholder name suffices.
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "acme", Environment: "dev", RepoPath: t.TempDir()}}
	session := connectTestMCPSession(t, model.BuildInfo{Version: "1.2.3"}, runtime)

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
