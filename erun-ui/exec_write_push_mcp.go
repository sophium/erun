package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// exec_write_push_mcp.go reaches the runtime pod's exec_commit/exec_push MCP
// tools the same way diff_mcp.go already reaches exec_diff (as "diff") —
// session.CallTool over the env's own MCP edge — so the desktop can push a
// review's source branch itself instead of requiring the operator to drop to
// the CLI first.

// execCommitViaMCP commits the runtime repo's working tree via the exec_commit
// tool. wait is left at its default (synchronous), matching how the CLI's own
// `erun exec commit` behaves today.
func execCommitViaMCP(ctx context.Context, endpoint, bearer, branch, message string) (eruncommon.CommitWorkingTreeResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return eruncommon.CommitWorkingTreeResult{}, err
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
		return eruncommon.CommitWorkingTreeResult{}, err
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.CommitWorkingTreeResult{}, err
	}
	var output struct {
		Commit *eruncommon.CommitWorkingTreeResult `json:"commit"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return eruncommon.CommitWorkingTreeResult{}, err
	}
	if output.Commit == nil {
		return eruncommon.CommitWorkingTreeResult{}, fmt.Errorf("exec_commit returned no result")
	}
	return *output.Commit, nil
}

// execPushViaMCP pushes the runtime repo's current branch via the exec_push
// tool.
func execPushViaMCP(ctx context.Context, endpoint, bearer, branch, remote string) (eruncommon.PushWorkingTreeBranchResult, error) {
	client := mcp.NewClient(&mcp.Implementation{Name: "erun-app", Version: currentBuildInfo().Version}, nil)
	session, err := client.Connect(ctx, mcpClientTransport(endpoint, bearer, false), nil)
	if err != nil {
		return eruncommon.PushWorkingTreeBranchResult{}, err
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
		return eruncommon.PushWorkingTreeBranchResult{}, err
	}
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		return eruncommon.PushWorkingTreeBranchResult{}, err
	}
	var output struct {
		Push *eruncommon.PushWorkingTreeBranchResult `json:"push"`
	}
	if err := json.Unmarshal(data, &output); err != nil {
		return eruncommon.PushWorkingTreeBranchResult{}, err
	}
	if output.Push == nil {
		return eruncommon.PushWorkingTreeBranchResult{}, fmt.Errorf("exec_push returned no result")
	}
	return *output.Push, nil
}
