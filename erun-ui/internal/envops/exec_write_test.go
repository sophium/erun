package envops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func execWritePushTestStore(t *testing.T) fakeStore {
	t.Helper()
	projectRoot := t.TempDir()
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]config.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}
}

func TestExecCommitCallsMCPWithTheClaimedBranchAndMessage(t *testing.T) {
	var gotEndpoint, gotBranch, gotMessage string
	service := newTestService(Deps{
		Store:               execWritePushTestStore(t),
		CanConnectLocalPort: func(int) bool { return true },
		CanReachMCPEndpoint: func(int) bool { return true },
		ExecCommit: func(_ context.Context, endpoint, _, branch, message string) (model.CommitWorkingTreeResult, error) {
			gotEndpoint, gotBranch, gotMessage = endpoint, branch, message
			return model.CommitWorkingTreeResult{Branch: branch, Commit: "abc123", Files: []string{"a.txt"}}, nil
		},
	})

	result, err := service.ExecCommit(uimodel.Selection{Tenant: "erun", Environment: "local"}, ExecCommitInput{
		Branch: " feature/1348-x ", Message: " open the review ",
	})
	if err != nil {
		t.Fatalf("ExecCommit failed: %v", err)
	}
	if gotEndpoint != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint: %q", gotEndpoint)
	}
	if gotBranch != "feature/1348-x" || gotMessage != "open the review" {
		t.Fatalf("expected trimmed branch/message, got branch=%q message=%q", gotBranch, gotMessage)
	}
	if result.Commit != "abc123" {
		t.Fatalf("unexpected commit result: %+v", result)
	}
}

func TestExecCommitRequiresAMessage(t *testing.T) {
	service := newTestService(Deps{
		Store:               execWritePushTestStore(t),
		CanConnectLocalPort: func(int) bool { return true },
		CanReachMCPEndpoint: func(int) bool { return true },
		ExecCommit: func(context.Context, string, string, string, string) (model.CommitWorkingTreeResult, error) {
			t.Fatal("ExecCommit must not be called without a message")
			return model.CommitWorkingTreeResult{}, nil
		},
	})

	_, err := service.ExecCommit(uimodel.Selection{Tenant: "erun", Environment: "local"}, ExecCommitInput{Branch: "feature/1348-x", Message: "   "})
	if err == nil || !strings.Contains(err.Error(), "commit message is required") {
		t.Fatalf("expected a message-required error, got %v", err)
	}
}

func TestExecCommitReturnsUnreachableWhenPortClosed(t *testing.T) {
	calls := 0
	service := newTestService(Deps{
		Store:               execWritePushTestStore(t),
		CanConnectLocalPort: func(int) bool { return false },
		CanReachMCPEndpoint: func(int) bool { return false },
		ExecCommit: func(context.Context, string, string, string, string) (model.CommitWorkingTreeResult, error) {
			calls++
			return model.CommitWorkingTreeResult{}, nil
		},
	})

	_, err := service.ExecCommit(uimodel.Selection{Tenant: "erun", Environment: "local"}, ExecCommitInput{Branch: "feature/1348-x", Message: "open the review"})
	if err == nil || !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("ExecCommit must not attempt the MCP call when the port is closed; got %d calls", calls)
	}
}

func TestExecPushCallsMCPWithTheClaimedBranch(t *testing.T) {
	var gotBranch, gotRemote string
	service := newTestService(Deps{
		Store:               execWritePushTestStore(t),
		CanConnectLocalPort: func(int) bool { return true },
		CanReachMCPEndpoint: func(int) bool { return true },
		ExecPush: func(_ context.Context, _, _, branch, remote string) (model.PushWorkingTreeBranchResult, error) {
			gotBranch, gotRemote = branch, remote
			return model.PushWorkingTreeBranchResult{Branch: branch, Remote: "origin", Commit: "abc123"}, nil
		},
	})

	result, err := service.ExecPush(uimodel.Selection{Tenant: "erun", Environment: "local"}, ExecPushInput{Branch: " feature/1348-x "})
	if err != nil {
		t.Fatalf("ExecPush failed: %v", err)
	}
	if gotBranch != "feature/1348-x" || gotRemote != "" {
		t.Fatalf("expected trimmed branch and default remote, got branch=%q remote=%q", gotBranch, gotRemote)
	}
	if result.Remote != "origin" {
		t.Fatalf("unexpected push result: %+v", result)
	}
}

func TestExecPushRequiresABranch(t *testing.T) {
	service := newTestService(Deps{Store: execWritePushTestStore(t)})

	_, err := service.ExecPush(uimodel.Selection{Tenant: "erun", Environment: "local"}, ExecPushInput{Branch: "  "})
	if err == nil || !strings.Contains(err.Error(), "branch is required") {
		t.Fatalf("expected a branch-required error, got %v", err)
	}
}
