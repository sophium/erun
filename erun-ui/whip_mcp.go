package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
)

// whipEnvironmentViaMCP lets the pod decide and push. tenant/environment are
// restated so a stale edge surfaces as a mismatch, not a wrong-target write.
func whipEnvironmentViaMCP(ctx context.Context, tenant, environment, endpoint, bearer string) (model.WhipResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return model.WhipResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "whip",
		Arguments: map[string]any{"preview": false, "tenant": tenant, "environment": environment},
	})
	if err != nil {
		return model.WhipResult{}, formatWhipMCPError(err)
	}
	return decodeWhipMCPResult(result)
}

func decodeWhipMCPResult(result *mcp.CallToolResult) (model.WhipResult, error) {
	// Tool errors arrive as IsError with no StructuredContent, not as a JSON-RPC error.
	if result.IsError {
		return model.WhipResult{}, formatWhipMCPError(fmt.Errorf("%s", mcpResultText(result)))
	}
	// Unmarshalling null is a no-op and would yield a fake zero result.
	if result.StructuredContent == nil {
		return model.WhipResult{}, fmt.Errorf("whip: environment returned no result")
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return model.WhipResult{}, err
	}
	var decoded model.WhipResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		return model.WhipResult{}, err
	}
	// The pod always stamps Candidate.ID; empty means not a real decision.
	if decoded.Candidate.ID == "" {
		return model.WhipResult{}, fmt.Errorf("whip: environment returned an empty result")
	}
	return decoded, nil
}

func mcpResultText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			return text.Text
		}
	}
	return "whip tool reported an error with no detail"
}

// formatWhipMCPError maps a missing tool on an old runtime image to a fix hint.
func formatWhipMCPError(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.Contains(lower, "method not found"),
		strings.Contains(lower, "unknown method"),
		strings.Contains(lower, "unknown tool"),
		strings.Contains(lower, "tool not found"),
		strings.Contains(lower, "-32601"),
		strings.Contains(lower, "no such tool"):
		return fmt.Errorf(
			"whip: this environment's runtime image does not yet support the whip tool. " +
				"Rebuild and redeploy the env (erun deploy <tenant> <env>) so the pod runs " +
				"a runtime image that includes it")
	}
	return fmt.Errorf("whip: %s", msg)
}
