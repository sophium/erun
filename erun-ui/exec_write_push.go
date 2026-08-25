package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// exec_write_push.go exposes the runtime repo's commit/push primitives to the
// desktop, the same precondition `erun-cli/cmd/exec.go` and the MCP
// `exec_commit`/`exec_push` tools already give the CLI and agents — reached
// the way `LoadDiff` (terminal_sessions.go) already reaches exec_diff, so a
// review's source branch can be pushed from the app instead of requiring a
// drop to the CLI first (#1348).

// uiExecCommitInput commits every change in the runtime repo's working tree.
// Branch is the caller's belief about the current branch, verified server-side
// and refused loudly on mismatch — the same contract exec_commit's MCP tool
// and `erun exec commit` already enforce.
type uiExecCommitInput struct {
	Branch  string `json:"branch"`
	Message string `json:"message"`
}

// uiExecPushInput pushes the runtime repo's current branch to a remote.
type uiExecPushInput struct {
	Branch string `json:"branch"`
	Remote string `json:"remote,omitempty"`
}

// withDefaultExecWriteDeps wires the exec_commit/exec_push MCP defaults, kept
// out of withDefaultWorkspaceDeps so that function stays under the module's
// cyclomatic-complexity cap.
func withDefaultExecWriteDeps(deps erunUIDeps) erunUIDeps {
	if deps.execCommit == nil {
		deps.execCommit = execCommitViaMCP
	}
	if deps.execPush == nil {
		deps.execPush = execPushViaMCP
	}
	return deps
}

// validateExecCommitInput trims and checks ExecCommit's input, kept apart
// from the call itself so ExecCommit's own branching stays under the
// module's cyclomatic-complexity cap.
func validateExecCommitInput(selection uiSelection, input uiExecCommitInput) (branch, message string, err error) {
	if selection.Tenant == "" || selection.Environment == "" {
		return "", "", fmt.Errorf("tenant and environment are required")
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

// ExecCommit commits the selected environment's working tree so its branch
// can be pushed and opened as a review. A real, immediate write: there is no
// preview path on the desktop, matching every other side-effecting dashboard
// action.
func (a *App) ExecCommit(selection uiSelection, input uiExecCommitInput) (eruncommon.CommitWorkingTreeResult, error) {
	selection = normalizeSelection(selection)
	branch, message, err := validateExecCommitInput(selection, input)
	if err != nil {
		return eruncommon.CommitWorkingTreeResult{}, err
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.CommitWorkingTreeResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return eruncommon.CommitWorkingTreeResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(eruncommon.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	commit, err := a.deps.execCommit(ctx, endpoint, bearer, branch, message)
	if err != nil && isMCPDialFailure(err) {
		return eruncommon.CommitWorkingTreeResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return commit, err
}

// ExecPush pushes the selected environment's current branch to its remote —
// the precondition a review's sourceBranch requires before CreateReview can
// reference it.
func (a *App) ExecPush(selection uiSelection, input uiExecPushInput) (eruncommon.PushWorkingTreeBranchResult, error) {
	selection = normalizeSelection(selection)
	branch := strings.TrimSpace(input.Branch)
	if selection.Tenant == "" || selection.Environment == "" {
		return eruncommon.PushWorkingTreeBranchResult{}, fmt.Errorf("tenant and environment are required")
	}
	if branch == "" {
		return eruncommon.PushWorkingTreeBranchResult{}, fmt.Errorf("branch is required")
	}
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return eruncommon.PushWorkingTreeBranchResult{}, err
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint != nil && !a.deps.canReachMCPEndpoint(mcpPort) {
		return eruncommon.PushWorkingTreeBranchResult{}, wrapMCPUnreachableErrorWithKind(
			a.classifyMCPUnreachable(mcpPort),
			errors.New(eruncommon.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := mcpEndpointForOpenResult(result)
	bearer := a.mcpBearer(result.Tenant, result.EnvConfig.Name)
	push, err := a.deps.execPush(ctx, endpoint, bearer, branch, strings.TrimSpace(input.Remote))
	if err != nil && isMCPDialFailure(err) {
		return eruncommon.PushWorkingTreeBranchResult{}, wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
	}
	return push, err
}
