package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ReconcileBypassToolResult is the reconcile-bypass tool's result.
type ReconcileBypassToolResult struct {
	Preview bool                             `json:"preview"`
	Result  eruncommon.ReconcileBypassResult `json:"result,omitempty"`
	Trace   []string                         `json:"trace,omitempty"`
}

type ExecReconcileBypassInput struct {
	platformAliasInput
	RemoteURL      string   `json:"remoteUrl,omitempty" jsonschema:"the github.com remote the ruleset lives on; defaults to the current checkout's origin"`
	RulesetID      int64    `json:"rulesetId" jsonschema:"the ruleset to check bypasses against"`
	TargetBranch   string   `json:"targetBranch" jsonschema:"the ruleset's protected branch"`
	Since          string   `json:"since,omitempty" jsonschema:"narrow the github lookup window: hour, day, week, or month; defaults to github's own window"`
	ExpectedActors []string `json:"expectedActors,omitempty" jsonschema:"identities allowed to hold the bypass grant; any other actor's bypass is reported UNEXPECTED_ACTOR"`
}

func execReconcileBypassTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_reconcile-bypass"
		result, err := eruncommon.ReconcileBypass(ctx, runtime.Store, input.Alias, eruncommon.ReconcileBypassParams{
			RemoteURL:      input.RemoteURL,
			RulesetID:      input.RulesetID,
			TargetBranch:   input.TargetBranch,
			Since:          input.Since,
			ExpectedActors: input.ExpectedActors,
		}, cloudDependencies(), eruncommon.ReconcileBypassDependencies{})
		if err != nil {
			return nil, ReconcileBypassToolResult{}, err
		}
		return nil, ReconcileBypassToolResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
