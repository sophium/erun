package cmd

import (
	"context"
	"fmt"

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

// environmentJobEnvelopeResult mirrors exec_raw's response, which -- unlike
// exec_agent's -- does not carry the full job record inline: exec_raw is
// also called synchronously (wait: true), so its response shape has to cover
// both cases, and the terse one only confirms a background start happened.
type environmentJobEnvelopeResult struct {
	JobID string `json:"jobId"`
	State string `json:"state"`
	Wait  bool   `json:"wait"`
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

// startJobInEnvironment starts job_start's replacement remotely: exec_agent
// for an agent run, exec_raw with wait: false for a plain command. Neither
// tool's immediate response carries the full job record the way job_start's
// did (exec_raw's does not carry one at all, since it is also used
// synchronously), so the command branch follows up with one exec_job_status
// call to return the same complete record callers of this function expect.
func startJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	if err := common.ValidateEnvironmentJobStart(params); err != nil {
		return common.EnvironmentJob{}, false, err
	}
	if params.Agent != "" {
		return startAgentJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	return startCommandJobInEnvironment(ctx, commandCtx, resolveOpen, params)
}

func startAgentJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "agent", params.Agent)
	putEnvironmentToolArgument(arguments, "prompt", params.Prompt)
	putEnvironmentToolArgument(arguments, "dir", params.Dir)
	putEnvironmentToolArgument(arguments, "env", params.Env)
	putEnvironmentToolArgument(arguments, "maxOutputBytes", params.MaxOutputBytes)
	putEnvironmentToolArgument(arguments, "leaseTtlSeconds", leaseTTLSeconds(params.LeaseTTL))
	result, resolved, err := callEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_agent", arguments, false)
	return result.Job, resolved, err
}

func startCommandJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.StartEnvironmentJobParams) (common.EnvironmentJob, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "name", params.Name)
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "command", params.Command)
	putEnvironmentToolArgument(arguments, "dir", params.Dir)
	putEnvironmentToolArgument(arguments, "env", params.Env)
	putEnvironmentToolArgument(arguments, "maxOutputBytes", params.MaxOutputBytes)
	putEnvironmentToolArgument(arguments, "leaseTtlSeconds", leaseTTLSeconds(params.LeaseTTL))
	arguments["wait"] = false
	started, resolved, err := callEnvironmentTool[environmentJobEnvelopeResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_raw", arguments, false)
	if err != nil || !resolved {
		return common.EnvironmentJob{}, resolved, err
	}
	if started.JobID == "" {
		return common.EnvironmentJob{}, resolved, fmt.Errorf("%s/%s started the job but returned no jobId", params.Tenant, params.Environment)
	}
	status, resolved, err := jobStatusFromEnvironment(ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, started.JobID)
	if err != nil || !resolved {
		return common.EnvironmentJob{}, resolved, err
	}
	if status.Job == nil {
		return common.EnvironmentJob{}, resolved, fmt.Errorf("%s/%s returned no job for id %q", params.Tenant, params.Environment, started.JobID)
	}
	return *status.Job, resolved, nil
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
	result, resolved, err := callEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_attach", arguments, false)
	return result.Job, resolved, err
}

func jobStatusFromEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, tenant, environment, id string) (environmentJobStatusResult, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", id)
	return callEnvironmentTool[environmentJobStatusResult](ctx, commandCtx, resolveOpen, tenant, environment, "exec_job_status", arguments, true)
}

func awaitJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.AwaitEnvironmentJobParams) (common.AwaitEnvironmentJobResult, bool, error) {
	if err := common.ValidateEnvironmentJobAwaitTimeout(params.Timeout); err != nil {
		return common.AwaitEnvironmentJobResult{}, false, err
	}
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "timeoutSeconds", int64(params.Timeout.Seconds()))
	return callEnvironmentTool[common.AwaitEnvironmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_await", arguments, true)
}

func jobOutputFromEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.ReadEnvironmentJobOutputParams) (common.EnvironmentJobOutput, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "offset", params.Offset)
	putEnvironmentToolArgument(arguments, "maxBytes", params.MaxBytes)
	return callEnvironmentTool[common.EnvironmentJobOutput](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_output", arguments, true)
}

func cancelJobInEnvironment(ctx context.Context, commandCtx common.Context, resolveOpen OpenResolver, params common.CancelEnvironmentJobParams) (common.CancelEnvironmentJobResult, bool, error) {
	arguments := map[string]any{}
	putEnvironmentToolArgument(arguments, "id", params.ID)
	putEnvironmentToolArgument(arguments, "signal", params.Signal)
	result, resolved, err := callEnvironmentTool[environmentJobCancelResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_cancel", arguments, false)
	return result.Cancel, resolved, err
}
