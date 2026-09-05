package erunmcp

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// ExecPlanRulesetBypassInput plans narrowing a protected branch's ruleset
// bypass grant to one non-human queue identity. It only ever reads from
// GitHub: the payloads it resolves are applied by a human, since a ruleset
// governs every contributor's merges.
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
	Preview bool                               `json:"preview"`
	Result  eruncommon.PlanRulesetBypassResult `json:"result,omitempty"`
	Trace   []string                           `json:"trace,omitempty"`
}

func execPlanRulesetBypassTool(_ RuntimeConfig) func(context.Context, *mcp.CallToolRequest, ExecPlanRulesetBypassInput) (*mcp.CallToolResult, PlanRulesetBypassToolResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input ExecPlanRulesetBypassInput) (*mcp.CallToolResult, PlanRulesetBypassToolResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		result, err := eruncommon.PlanRulesetBypass(ctx, eruncommon.PlanRulesetBypassParams{
			RemoteURL:      input.RemoteURL,
			RulesetID:      input.RulesetID,
			TargetBranch:   input.TargetBranch,
			QueueActorType: input.QueueActorType,
			QueueActor:     input.QueueActor,
			OutDir:         input.OutDir,
		}, eruncommon.PlanRulesetBypassDependencies{})
		if err != nil {
			return nil, PlanRulesetBypassToolResult{}, err
		}
		return nil, PlanRulesetBypassToolResult{Preview: input.Preview, Result: result, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
