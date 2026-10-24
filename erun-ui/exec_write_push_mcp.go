package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/infra/gitx"
)

// execCommitViaMCP calls exec_commit synchronously, like `erun exec commit`.
func execCommitViaMCP(ctx context.Context, endpoint, bearer, branch, message string) (gitx.CommitWorkingTreeResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return gitx.CommitWorkingTreeResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "exec_commit",
		Arguments: map[string]any{
			"branch":  strings.TrimSpace(branch),
			"message": message,
		},
	})
	if err != nil {
		return gitx.CommitWorkingTreeResult{}, err
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return gitx.CommitWorkingTreeResult{}, err
	}
	var output struct {
		Commit *gitx.CommitWorkingTreeResult `json:"commit"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return gitx.CommitWorkingTreeResult{}, err
	}
	if output.Commit == nil {
		return gitx.CommitWorkingTreeResult{}, fmt.Errorf("exec_commit returned no result")
	}
	return *output.Commit, nil
}

func execPushViaMCP(ctx context.Context, endpoint, bearer, branch, remote string) (gitx.PushWorkingTreeBranchResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return gitx.PushWorkingTreeBranchResult{}, err
	}
	defer func() {
		_ = session.Close()
	}()

	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "exec_push",
		Arguments: map[string]any{
			"branch": strings.TrimSpace(branch),
			"remote": strings.TrimSpace(remote),
		},
	})
	if err != nil {
		return gitx.PushWorkingTreeBranchResult{}, err
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return gitx.PushWorkingTreeBranchResult{}, err
	}
	var output struct {
		Push *gitx.PushWorkingTreeBranchResult `json:"push"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return gitx.PushWorkingTreeBranchResult{}, err
	}
	if output.Push == nil {
		return gitx.PushWorkingTreeBranchResult{}, fmt.Errorf("exec_push returned no result")
	}
	return *output.Push, nil
}
