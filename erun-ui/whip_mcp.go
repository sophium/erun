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
func whipEnvironmentViaMCP(ctx context.Context, endpoint, bearer string) (eruncommon.WhipResult, error) {
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
		Arguments: map[string]any{"preview": false},
	})
	if err != nil {
		return eruncommon.WhipResult{}, formatWhipMCPError(err)
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.WhipResult{}, err
	}
	var decoded eruncommon.WhipResult
	if err := json.Unmarshal(data, &decoded); err != nil {
		return eruncommon.WhipResult{}, err
	}
	return decoded, nil
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
