package erunmcp

import (
	"context"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-mcp/tools/activity"
	"github.com/sophium/erun/erun-mcp/tools/agent"
	"github.com/sophium/erun/erun-mcp/tools/exectool"
	"github.com/sophium/erun/erun-mcp/tools/jobs"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// Work runs in this pod, so a foreign target must be refused.
func TestJobLeaseAndIdleToolsRefuseAForeignEnvironment(t *testing.T) {
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	// Several tools fail downstream on a foreign target, so assert the specific refusal.
	_, _, err := jobs.JobStatusTool(runtime)(ctx, nil, jobs.JobStatusInput{Tenant: "tenant-b", Environment: "prod", ID: "x"})
	assertRefusedForeignTarget(t, "job_status", err)

	_, _, err = jobs.JobAwaitTool(runtime)(ctx, nil, jobs.JobAwaitInput{Tenant: "tenant-b", Environment: "prod", ID: "x"})
	assertRefusedForeignTarget(t, "job_await", err)

	_, _, err = activity.ActivityLeaseTakeTool(runtime)(ctx, nil, activity.ActivityLeaseTakeInput{Tenant: "tenant-b", Environment: "prod", Name: "x"})
	assertRefusedForeignTarget(t, "activity_lease_take", err)

	_, _, err = idleStopHistoryTool(runtime)(ctx, nil, IdleStopHistoryInput{Tenant: "tenant-b", Environment: "prod"})
	assertRefusedForeignTarget(t, "idle_stop_history", err)

	_, _, err = exectool.RawTool(runtime)(ctx, nil, exectool.RawInput{
		Command: []string{"true"}, Wait: boolPtr(false), Tenant: "tenant-b", Environment: "prod",
	})
	assertRefusedForeignTarget(t, "exec_raw (wait:false)", err)

	_, _, err = agent.AgentTool(runtime)(ctx, nil, agent.AgentInput{
		Agent: "claude", Prompt: "x", Tenant: "tenant-b", Environment: "prod",
	})
	assertRefusedForeignTarget(t, "exec_agent", err)
}

// assertRefusedForeignTarget fails on any other error: it means the tool acted first.
func assertRefusedForeignTarget(t *testing.T, tool string, err error) {
	t.Helper()
	if err == nil {
		t.Errorf("%s accepted a foreign environment instead of refusing it", tool)
		return
	}
	if !strings.Contains(err.Error(), "tenant-a/dev") || !strings.Contains(err.Error(), "tenant-b/prod") {
		t.Errorf("%s failed for the wrong reason -- want a refusal naming tenant-a/dev and tenant-b/prod, got: %v", tool, err)
	}
}

// exec_raw (wait:false) and exec_agent must resolve through resolveLocalTarget like the
// other job tools.
func TestExecRawBackgroundAndExecAgentResolveTenantEnvironmentLikeOtherJobTools(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	// ensureAgentJobToolAvailable resolves "claude" before the preview short-circuit.
	t.Setenv("ERUN_CLAUDE_BIN", "true")
	runtime := normalizeRuntimeConfig(shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev", RepoPath: t.TempDir()}})
	ctx := context.Background()

	for _, tc := range []struct {
		name        string
		tenant      string
		environment string
	}{
		{"omitted", "", ""},
		{"restated", "tenant-a", "dev"},
	} {
		t.Run("exec_raw/"+tc.name, func(t *testing.T) {
			if _, _, err := exectool.RawTool(runtime)(ctx, nil, exectool.RawInput{
				Command: []string{"true"}, Wait: boolPtr(false), Preview: true,
				Tenant: tc.tenant, Environment: tc.environment,
			}); err != nil {
				t.Errorf("exec_raw (wait:false) refused %s scope: %v", tc.name, err)
			}
		})
		t.Run("exec_agent/"+tc.name, func(t *testing.T) {
			if _, _, err := agent.AgentTool(runtime)(ctx, nil, agent.AgentInput{
				Agent: "claude", Prompt: "x", Preview: true,
				Tenant: tc.tenant, Environment: tc.environment,
			}); err != nil {
				t.Errorf("exec_agent refused %s scope: %v", tc.name, err)
			}
		})
	}
}
