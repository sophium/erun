package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
)

// uiExecCommitInput.Branch is verified server-side and refused on mismatch.
type uiExecCommitInput struct {
	Branch  string `json:"branch"`
	Message string `json:"message"`
}

type uiExecPushInput struct {
	Branch string `json:"branch"`
	Remote string `json:"remote,omitempty"`
}

// withDefaultExecWriteDeps is split out to stay under the cyclomatic-complexity cap.
func withDefaultExecWriteDeps(deps erunUIDeps) erunUIDeps {
	if deps.execCommit == nil {
		deps.execCommit = execCommitViaMCP
	}
	if deps.execPush == nil {
		deps.execPush = execPushViaMCP
	}
	return deps
}

func validateExecCommitInput(selection uiSelection, input uiExecCommitInput) (branch, message string, err error) {
	if err := errMissingTenantOrEnvironment("commit environment working tree", selection.Tenant, selection.Environment); err != nil {
		return "", "", err
	}
	branch = strings.TrimSpace(input.Branch)
	if branch == "" {
		return "", "", fmt.Errorf("branch is required")
	}
	message = strings.TrimSpace(input.Message)
	if message == "" {
		return "", "", fmt.Errorf("commit message is required")
	}
	return branch, message, nil
}

// ExecCommit commits the selected environment's working tree immediately; there
// is no preview.
func (a *App) ExecCommit(selection uiSelection, input uiExecCommitInput) (model.CommitWorkingTreeResult, error) {
	selection = normalizeSelection(selection)
	branch, message, err := validateExecCommitInput(selection, input)
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := config.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return model.CommitWorkingTreeResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	commit, err := a.deps.execCommit(ctx, endpoint, bearer, branch, message)
	if err != nil && isMCPDialFailure(err) {
		return model.CommitWorkingTreeResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return commit, err
}

// ExecPush pushes the selected environment's current branch to its remote.
func (a *App) ExecPush(selection uiSelection, input uiExecPushInput) (model.PushWorkingTreeBranchResult, error) {
	selection = normalizeSelection(selection)
	branch := strings.TrimSpace(input.Branch)
	if err := errMissingTenantOrEnvironment("push environment branch", selection.Tenant, selection.Environment); err != nil {
		return model.PushWorkingTreeBranchResult{}, err
	}
	if branch == "" {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("branch is required")
	}
	result, err := resolve.ResolveOpen(a.deps.store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.PushWorkingTreeBranchResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := config.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return model.PushWorkingTreeBranchResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	push, err := a.deps.execPush(ctx, endpoint, bearer, branch, strings.TrimSpace(input.Remote))
	if err != nil && isMCPDialFailure(err) {
		return model.PushWorkingTreeBranchResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return push, err
}
