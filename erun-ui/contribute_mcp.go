package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cloneERunViaMCP has the env clone the ERun repo and install the contribute
// shim. The idle-probe transport keeps this call from resetting the env's idle
// activity marker.
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
