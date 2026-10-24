package exectool

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

type ReconcileBypassToolResult struct {
	Preview bool                         `json:"preview"`
	Result  review.ReconcileBypassResult `json:"result,omitempty"`
	Trace   []string                     `json:"trace,omitempty"`
}

type ExecReconcileBypassInput struct {
	shared.PlatformAliasInput
	RemoteURL      string   `json:"remoteUrl,omitempty" jsonschema:"the github.com remote the ruleset lives on; defaults to the current checkout's origin"`
	RulesetID      int64    `json:"rulesetId" jsonschema:"the ruleset to check bypasses against"`
	TargetBranch   string   `json:"targetBranch" jsonschema:"the ruleset's protected branch"`
	Since          string   `json:"since,omitempty" jsonschema:"narrow the github lookup window: hour, day, week, or month; defaults to github's own window"`
	ExpectedActors []string `json:"expectedActors,omitempty" jsonschema:"identities allowed to hold the bypass grant; any other actor's bypass is reported UNEXPECTED_ACTOR"`
}

func ExecReconcileBypassTool(runtime shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecReconcileBypassInput) (*mcp.CallToolResult, ReconcileBypassToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := shared.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "exec_reconcile-bypass"
		result, err := review.ReconcileBypass(ctx, runtime.Store, input.Alias, review.ReconcileBypassParams{
			RemoteURL:      input.RemoteURL,
			RulesetID:      input.RulesetID,
			TargetBranch:   input.TargetBranch,
			Since:          input.Since,
			ExpectedActors: input.ExpectedActors,
		}, shared.CloudDependencies(), review.ReconcileBypassDependencies{})
		if err != nil {
			return nil, ReconcileBypassToolResult{}, err
		}
		return nil, ReconcileBypassToolResult{Preview: input.Preview, Result: result, Trace: shared.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
