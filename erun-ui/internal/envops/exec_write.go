package envops

import (
	"errors"
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// ExecCommitInput.Branch is verified server-side and refused on mismatch.
type ExecCommitInput struct {
	Branch  string `json:"branch"`
	Message string `json:"message"`
}

type ExecPushInput struct {
	Branch string `json:"branch"`
	Remote string `json:"remote,omitempty"`
}

func validateExecCommitInput(selection uimodel.Selection, input ExecCommitInput) (branch, message string, err error) {
	if err := uimodel.ErrMissingTenantOrEnvironment("commit environment working tree", selection.Tenant, selection.Environment); err != nil {
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
func (s *Service) ExecCommit(selection uimodel.Selection, input ExecCommitInput) (model.CommitWorkingTreeResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	branch, message, err := validateExecCommitInput(selection, input)
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.CommitWorkingTreeResult{}, err
	}
	ctx := s.d.Context()
	mcpPort := config.MCPPortForResult(result)
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		return model.CommitWorkingTreeResult{}, s.d.WrapMCPUnreachable(
			s.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := s.d.MCPEndpoint(result)
	bearer := s.d.MCPBearer(result.Tenant, result.EnvConfig.Name)
	commit, err := s.d.ExecCommit(ctx, endpoint, bearer, branch, message)
	if err != nil && s.d.IsMCPDialFailure(err) {
		return model.CommitWorkingTreeResult{}, s.d.WrapMCPUnreachable(s.classifyMCPUnreachable(mcpPort), err)
	}
	return commit, err
}

// ExecPush pushes the selected environment's current branch to its remote.
func (s *Service) ExecPush(selection uimodel.Selection, input ExecPushInput) (model.PushWorkingTreeBranchResult, error) {
	selection = uimodel.NormalizeSelection(selection)
	branch := strings.TrimSpace(input.Branch)
	if err := uimodel.ErrMissingTenantOrEnvironment("push environment branch", selection.Tenant, selection.Environment); err != nil {
		return model.PushWorkingTreeBranchResult{}, err
	}
	if branch == "" {
		return model.PushWorkingTreeBranchResult{}, fmt.Errorf("branch is required")
	}
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      selection.Tenant,
		Environment: selection.Environment,
	})
	if err != nil {
		return model.PushWorkingTreeBranchResult{}, err
	}
	ctx := s.d.Context()
	mcpPort := config.MCPPortForResult(result)
	if !s.d.CanReachMCPEndpoint(mcpPort) {
		return model.PushWorkingTreeBranchResult{}, s.d.WrapMCPUnreachable(
			s.classifyMCPUnreachable(mcpPort),
			errors.New(mcp.DescribeLocalMCPUnreachable(result.Tenant, result.EnvConfig.Name, mcpPort)),
		)
	}
	endpoint := s.d.MCPEndpoint(result)
	bearer := s.d.MCPBearer(result.Tenant, result.EnvConfig.Name)
	push, err := s.d.ExecPush(ctx, endpoint, bearer, branch, strings.TrimSpace(input.Remote))
	if err != nil && s.d.IsMCPDialFailure(err) {
		return model.PushWorkingTreeBranchResult{}, s.d.WrapMCPUnreachable(s.classifyMCPUnreachable(mcpPort), err)
	}
	return push, err
}
