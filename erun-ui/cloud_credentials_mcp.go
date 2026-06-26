package main

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func injectCloudHostCredentialsViaMCP(ctx context.Context, endpoint, bearer string, accessKeyID, secretAccessKey, sessionToken string, expiration time.Time) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()
	args := map[string]any{
		"accessKeyId":     accessKeyID,
		"secretAccessKey": secretAccessKey,
	}
	if sessionToken != "" {
		args["sessionToken"] = sessionToken
	}
	if !expiration.IsZero() {
		args["expiration"] = expiration.UTC().Format(time.RFC3339)
	}
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "cloud_inject_aws_credentials",
		Arguments: args,
	}); err != nil {
		return fmt.Errorf("cloud_inject_aws_credentials: %w", err)
	}
	return nil
}

func clearCloudHostCredentialsViaMCP(ctx context.Context, endpoint, bearer string) error {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = session.Close()
	}()
	if _, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name:      "cloud_clear_aws_credentials",
		Arguments: map[string]any{},
	}); err != nil {
		return fmt.Errorf("cloud_clear_aws_credentials: %w", err)
	}
	return nil
}
