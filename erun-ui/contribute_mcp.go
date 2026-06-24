package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cloneERunViaMCP calls the env's `contribute_clone` MCP tool to ensure
// the ERun repository is cloned into $HOME/git/erun inside the
// environment and the contribute shim is installed.
//
// Mirrors the call pattern used by loadDiffFromMCP / loadIdleStatusFromMCP
// so the same idle-probe round-tripper keeps the call from refreshing
// the env's idle activity marker.
func cloneERunViaMCP(ctx context.Context, endpoint, bearer string) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, true), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()

	_, err = session.CallTool(ctx, &mcp.CallToolParams{
		Name: "contribute_clone",
	})
	return err
}
