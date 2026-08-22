package cmd

import (
	"context"

	common "github.com/sophium/erun/erun-common"
)

// The job verbs run the work inside the environment wherever they are invoked
// from. Off-environment that means the environment's own job tools: the work,
// its log, and the activity lease that keeps idle-stop off it all belong to the
// environment, and a job supervised on the operator's laptop has none of them.
//
// These payloads mirror what the environment's job tools return. erun-cli and
// erun-mcp must not import each other, so the wire shape is restated here.

type environmentJobResult struct {
	Tenant      string                `json:"tenant"`
	Environment string                `json:"environment"`
	Job         common.EnvironmentJob `json:"job"`
	Executed    bool                  `json:"executed"`
}

type environmentJobStatusResult struct {
	Tenant      string                  `json:"tenant"`
	Environment string                  `json:"environment"`
	Job         *common.EnvironmentJob  `json:"job,omitempty"`
	Jobs        []common.EnvironmentJob `json:"jobs"`
}

type environmentJobCancelResult struct {
	Tenant      string                            `json:"tenant"`
	Environment string                            `json:"environment"`
	Cancel      common.CancelEnvironmentJobResult `json:"cancel"`
}

func startJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	if err := common.ValidateEnvironmentJobStart(params); err != nil {
		return common.EnvironmentJob{}, false, err
	}
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "command", params.Command)
	putEnvironmentToolArgument(arguments, "agent", params.Agent)
	putEnvironmentToolArgument(arguments, "prompt", params.Prompt)
	putEnvironmentToolArgument(arguments, "dir", params.Dir)
	putEnvironmentToolArgument(arguments, "env", params.Env)
	putEnvironmentToolArgument(arguments, "maxOutputBytes", params.MaxOutputBytes)
	putEnvironmentToolArgument(arguments, "leaseTtlSeconds", leaseTTLSeconds(params.LeaseTTL))
	result, resolved, err := callEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "job_start", arguments)
	return result.Job, resolved, err
}

func attachJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.AttachEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	if err := common.ValidateEnvironmentJobAttach(params); err != nil {
		return common.EnvironmentJob{}, false, err
	}
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "pid", params.PID)
	putEnvironmentToolArgument(arguments, "logPath", params.LogPath)
	putEnvironmentToolArgument(arguments, "leaseTtlSeconds", leaseTTLSeconds(params.LeaseTTL))
	result, resolved, err := callEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "job_attach", arguments)
	return result.Job, resolved, err
}

func jobStatusFromEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id string) (environmentJobStatusResult, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", id)
	return callEnvironmentTool[environmentJobStatusResult](ctx, commandCtx, resolveOpen, tenant, environment, "job_status", arguments)
}

func awaitJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.AwaitEnvironmentJobParams) (common.AwaitEnvironmentJobResult, bool, error) {
	if err := common.ValidateEnvironmentJobAwaitTimeout(params.Timeout); err != nil {
		return common.AwaitEnvironmentJobResult{}, false, err
	}
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "timeoutSeconds", int64(params.Timeout.Seconds()))
	return callEnvironmentTool[common.AwaitEnvironmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "job_await", arguments)
}

func jobOutputFromEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.ReadEnvironmentJobOutputParams) (common.EnvironmentJobOutput, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "offset", params.Offset)
	putEnvironmentToolArgument(arguments, "maxBytes", params.MaxBytes)
	return callEnvironmentTool[common.EnvironmentJobOutput](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "job_output", arguments)
}

func cancelJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.CancelEnvironmentJobParams) (common.CancelEnvironmentJobResult, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "signal", params.Signal)
	result, resolved, err := callEnvironmentTool[environmentJobCancelResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "job_cancel", arguments)
	return result.Cancel, resolved, err
}
