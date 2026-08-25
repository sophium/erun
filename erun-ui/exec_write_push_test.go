package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func execWritePushTestStore(t *testing.T) erunUIStore {
	t.Helper()
	projectRoot := t.TempDir()
	return stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
	app := NewApp(erunUIDeps{
		store:               execWritePushTestStore(t),
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		execCommit: func(_ context.Context, endpoint, _, branch, message string) (eruncommon.CommitWorkingTreeResult, error) {
			gotEndpoint, gotBranch, gotMessage = endpoint, branch, message
			return eruncommon.CommitWorkingTreeResult{Branch: branch, Commit: "abc123", Files: []string{"a.txt"}}, nil
		},
	})

	result, err := app.ExecCommit(uiSelection{Tenant: "erun", Environment: "local"}, uiExecCommitInput{
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
	app := NewApp(erunUIDeps{
		store:               execWritePushTestStore(t),
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		execCommit: func(context.Context, string, string, string, string) (eruncommon.CommitWorkingTreeResult, error) {
			t.Fatal("execCommit must not be called without a message")
			return eruncommon.CommitWorkingTreeResult{}, nil
		},
	})

	_, err := app.ExecCommit(uiSelection{Tenant: "erun", Environment: "local"}, uiExecCommitInput{Branch: "feature/1348-x", Message: "   "})
	if err == nil || !strings.Contains(err.Error(), "commit message is required") {
		t.Fatalf("expected a message-required error, got %v", err)
	}
}

func TestExecCommitReturnsUnreachableWhenPortClosed(t *testing.T) {
	calls := 0
	app := NewApp(erunUIDeps{
		store:               execWritePushTestStore(t),
		canConnectLocalPort: func(int) bool { return false },
		canReachMCPEndpoint: func(int) bool { return false },
		execCommit: func(context.Context, string, string, string, string) (eruncommon.CommitWorkingTreeResult, error) {
			calls++
			return eruncommon.CommitWorkingTreeResult{}, nil
		},
	})

	_, err := app.ExecCommit(uiSelection{Tenant: "erun", Environment: "local"}, uiExecCommitInput{Branch: "feature/1348-x", Message: "open the review"})
	if err == nil || !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if calls != 0 {
		t.Fatalf("ExecCommit must not attempt the MCP call when the port is closed; got %d calls", calls)
	}
}

func TestExecPushCallsMCPWithTheClaimedBranch(t *testing.T) {
	var gotBranch, gotRemote string
	app := NewApp(erunUIDeps{
		store:               execWritePushTestStore(t),
		canConnectLocalPort: func(int) bool { return true },
		canReachMCPEndpoint: func(int) bool { return true },
		execPush: func(_ context.Context, _, _, branch, remote string) (eruncommon.PushWorkingTreeBranchResult, error) {
			gotBranch, gotRemote = branch, remote
			return eruncommon.PushWorkingTreeBranchResult{Branch: branch, Remote: "origin", Commit: "abc123"}, nil
		},
	})

	result, err := app.ExecPush(uiSelection{Tenant: "erun", Environment: "local"}, uiExecPushInput{Branch: " feature/1348-x "})
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
	app := NewApp(erunUIDeps{store: execWritePushTestStore(t)})

	_, err := app.ExecPush(uiSelection{Tenant: "erun", Environment: "local"}, uiExecPushInput{Branch: "  "})
	if err == nil || !strings.Contains(err.Error(), "branch is required") {
		t.Fatalf("expected a branch-required error, got %v", err)
	}
}
