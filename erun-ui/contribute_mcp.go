package main

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// cloneERunViaMCP clones ERun and installs the contribute shim; the idle-probe
// transport keeps it from resetting the idle marker.
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
