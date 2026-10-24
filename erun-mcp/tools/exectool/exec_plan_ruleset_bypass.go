package exectool

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/review"
	"github.com/sophium/erun/erun-mcp/tools/shared"
)

// ExecPlanRulesetBypassInput plans narrowing a ruleset bypass to one queue
// identity; it only reads, and a human applies the result.
type ExecPlanRulesetBypassInput struct {
	RemoteURL      string `json:"remoteUrl,omitempty" jsonschema:"the github.com remote the ruleset lives on; defaults to the current checkout's origin"`
	RulesetID      int64  `json:"rulesetId" jsonschema:"the ruleset whose bypass grant is being narrowed"`
	TargetBranch   string `json:"targetBranch,omitempty" jsonschema:"the protected branch the ruleset must govern, checked against its own conditions"`
	QueueActorType string `json:"queueActorType,omitempty" jsonschema:"the queue identity's github actor type: User (default), Integration, Team, RepositoryRole, OrganizationAdmin, or DeployKey"`
	QueueActor     string `json:"queueActor" jsonschema:"the queue identity: a login for User, otherwise the numeric actor id"`
	OutDir         string `json:"outDir,omitempty" jsonschema:"directory the stage1, stage2 and rollback payload files are written to; defaults to the working directory"`
	Preview        bool   `json:"preview,omitempty" jsonschema:"when true, trace the lookups and the files it would write without sending or writing anything"`
	Verbosity      int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
}

// PlanRulesetBypassToolResult is the plan-ruleset-bypass tool's result.
type PlanRulesetBypassToolResult struct {
	Preview bool                          `json:"preview"`
	Result  model.PlanRulesetBypassResult `json:"result,omitempty"`
	Trace   []string                      `json:"trace,omitempty"`
}

func ExecPlanRulesetBypassTool(_ shared.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecPlanRulesetBypassInput) (*mcp.CallToolResult, PlanRulesetBypassToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecPlanRulesetBypassInput) (*mcp.CallToolResult, PlanRulesetBypassToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := shared.RuntimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		result, err := review.PlanRulesetBypass(ctx, model.PlanRulesetBypassParams{
			RemoteURL:      input.RemoteURL,
			RulesetID:      input.RulesetID,
			TargetBranch:   input.TargetBranch,
			QueueActorType: input.QueueActorType,
			QueueActor:     input.QueueActor,
			OutDir:         input.OutDir,
		}, review.PlanRulesetBypassDependencies{})
		if err != nil {
			return nil, PlanRulesetBypassToolResult{}, err
		}
		return nil, PlanRulesetBypassToolResult{Preview: input.Preview, Result: result, Trace: shared.NormalizeTraceLines(traceOutput.String())}, nil
	}
}
