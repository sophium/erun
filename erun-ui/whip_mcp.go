package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// whipEnvironmentViaMCP calls the environment's own "whip" MCP tool -- the
// same tool erun-cli's `erun whip` calls over its edge -- so the pod decides
// and pushes exactly as it would for any other transport; the desktop never
// re-derives that decision locally. Not idle-probed: pushing a nudge is a
// real write, not a diagnostic read.
//
// tenant/environment are the target whipOneEnvironmentNow already resolved
// and used to pick endpoint -- restating them in the call itself, rather than
// leaving the tool to infer them from the server's own bound context, is the
// stronger assertion the resolveLocalTarget contract (erun-mcp/runtime.go)
// asks callers to make: a stale edge pointed at the wrong environment then
// surfaces as a named mismatch instead of a silent act on the wrong one.
func whipEnvironmentViaMCP(ctx context.Context, tenant, environment, endpoint, bearer string) (eruncommon.WhipResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return eruncommon.WhipResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "whip",
		Arguments: map[string]any{"preview": false, "tenant": tenant, "environment": environment},
	})
	if err != nil {
		return eruncommon.WhipResult{}, formatWhipMCPError(err)
	}
	return decodeWhipMCPResult(result)
}

// decodeWhipMCPResult turns one "whip" tool CallToolResult into a
// eruncommon.WhipResult, pulled out of whipEnvironmentViaMCP so the decode
// path can be driven directly in tests without a live MCP round trip.
func decodeWhipMCPResult(result *mcp.CallToolResult) (eruncommon.WhipResult, error) {
	// A tool-reported failure comes back as a populated CallToolResult with
	// IsError set and no StructuredContent, not as a JSON-RPC error -- the SDK
	// server wraps a returned error this way (server.go's "for regular errors,
	// embed them in the tool result"). Missing this check is what let the
	// nil-StructuredContent case below decode straight into a zero-valued
	// success: CallTool's own err was nil, so nothing signalled failure at all.
	if result.IsError {
		return eruncommon.WhipResult{}, formatWhipMCPError(fmt.Errorf("%s", mcpResultText(result)))
	}
	// json.Unmarshal into a struct is a documented no-op for a nil/"null"
	// input, so a nil StructuredContent must be rejected before it decodes
	// into an indistinguishable-from-real zero WhipResult.
	if result.StructuredContent == nil {
		return eruncommon.WhipResult{}, fmt.Errorf("whip: environment returned no result")
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.WhipResult{}, err
	}
	var decoded eruncommon.WhipResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		return eruncommon.WhipResult{}, err
	}
	// The pod always stamps Candidate.ID/Name (RunLocalEnvironmentWhip); an
	// empty ID means the payload was not a real decision, whatever decoded.
	if decoded.Candidate.ID == "" {
		return eruncommon.WhipResult{}, fmt.Errorf("whip: environment returned an empty result")
	}
	return decoded, nil
}

// mcpResultText extracts the text an IsError result carries -- the SDK puts
// the tool's own error message in Content as a TextContent block (see
// CallToolResult.SetError) -- falling back to a generic phrase when a result
// somehow has none.
func mcpResultText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok && strings.TrimSpace(text.Text) != "" {
			return text.Text
		}
	}
	return "whip tool reported an error with no detail"
}

// formatWhipMCPError mirrors formatJobMCPError/formatIdleStopMCPError: a
// runtime image that predates the "whip" MCP tool gets one clear line
// pointing at the fix instead of a raw JSON-RPC "method not found".
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
