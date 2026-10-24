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

// Every job, lease and idle tool takes a tenant/environment, and none of them can
// honour one that names a different environment: the work runs in this pod,
// against this pod's repo and this pod's erun binary. Before this was enforced,
// job_start accepted a foreign target, echoed it back in its result, and ran here
// anyway -- so the caller was handed a handle asserting a target the work never
// reached (#1195). Each of these must refuse.
func TestJobLeaseAndIdleToolsRefuseAForeignEnvironment(t *testing.T) {
	runtime := shared.RuntimeConfig{Context: shared.RuntimeContext{Tenant: "tenant-a", Environment: "dev"}}
	ctx := context.Background()

	// Assert on the REFUSAL, not merely on "an error". Several of these tools fail
	// for unrelated downstream reasons when handed a foreign target -- a missing
	// job, an unresolvable repo -- so a bare err != nil passes even against the
	// unfixed code and proves nothing. The error has to be the one that names both
	// scopes.
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

// assertRefusedForeignTarget insists the tool refused BECAUSE the target is not
// this server's, naming the server's scope and the requested one. Any other
// error -- including a plausible downstream one -- is a failure, because it means
// the tool went on to act rather than refusing up front.
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

// TestExecRawBackgroundAndExecAgentResolveTenantEnvironmentLikeOtherJobTools
// guards against exec_raw's wait:false path and exec_agent reading
// runtime.Context.Tenant/Environment directly again, with no field a caller
// could use to supply or restate them and no foreign-target refusal --
// unlike every sibling job tool, which resolves through resolveLocalTarget.
// Preview mode and a placeholder supervisor path (never exec'd, since preview
// short-circuits before the process would start) let this exercise the same
// resolution the live call makes without spawning anything.
func TestExecRawBackgroundAndExecAgentResolveTenantEnvironmentLikeOtherJobTools(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", "erun-test-supervisor-stub")
	// ensureAgentJobToolAvailable resolves "claude" before the preview
	// short-circuit, so it needs a real binary on PATH even though nothing
	// actually execs it in this test.
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
