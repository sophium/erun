package execcmd

import (
	"context"
	"fmt"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/jobs"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
)

// Off-environment, job verbs use the environment's job tools. erun-cli and erun-mcp
// must not import each other, so these payloads restate the wire shape.

type environmentJobResult struct {
	Tenant      string                   `json:"tenant"`
	Environment string                   `json:"environment"`
	Job         sharedpkg.EnvironmentJob `json:"job"`
	Executed    bool                     `json:"executed"`
}

// environmentJobEnvelopeResult mirrors exec_raw's response, which carries no job record.
type environmentJobEnvelopeResult struct {
	JobID string `json:"jobId"`
	State string `json:"state"`
	Wait  bool   `json:"wait"`
}

type environmentJobStatusResult struct {
	Tenant      string                     `json:"tenant"`
	Environment string                     `json:"environment"`
	Job         *sharedpkg.EnvironmentJob  `json:"job,omitempty"`
	Jobs        []sharedpkg.EnvironmentJob `json:"jobs"`
}

type environmentJobCancelResult struct {
	Tenant      string                          `json:"tenant"`
	Environment string                          `json:"environment"`
	Cancel      jobs.CancelEnvironmentJobResult `json:"cancel"`
}

// startJobInEnvironment starts via exec_agent or exec_raw (wait: false); the command
// branch follows up with exec_job_status for the full record.
func startJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.StartEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	if err := jobs.ValidateEnvironmentJobStart(params); err != nil {
		return sharedpkg.EnvironmentJob{}, false, err
	}
	if params.Agent != "" {
		return startAgentJobInEnvironment(ctx, commandCtx, resolveOpen, params)
	}
	return startCommandJobInEnvironment(ctx, commandCtx, resolveOpen, params)
}

func startAgentJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.StartEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "name", params.Name)
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "agent", params.Agent)
	shared.PutEnvironmentToolArgument(arguments, "prompt", params.Prompt)
	shared.PutEnvironmentToolArgument(arguments, "dir", params.Dir)
	shared.PutEnvironmentToolArgument(arguments, "env", params.Env)
	shared.PutEnvironmentToolArgument(arguments, "maxOutputBytes", params.MaxOutputBytes)
	shared.PutEnvironmentToolArgument(arguments, "leaseTtlSeconds", shared.LeaseTTLSeconds(params.LeaseTTL))
	shared.PutEnvironmentToolArgument(arguments, "handoff", params.Handoff)
	shared.PutEnvironmentToolArgument(arguments, "exclusive", params.Exclusive)
	shared.PutEnvironmentToolArgument(arguments, "startedByJobId", startedByJobIDForOffEnvironmentStart(params))
	result, resolved, err := shared.CallEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_agent", arguments, false)
	if err != nil {
		return sharedpkg.EnvironmentJob{}, resolved, jobs.DescribeExclusiveJobStartVersionSkew(params.Tenant, params.Environment, params.Exclusive, err)
	}
	return result.Job, resolved, nil
}

// startedByJobIDForOffEnvironmentStart threads the parent job id explicitly, since
// the MCP edge has no ERUN_JOB_ID to inherit.
func startedByJobIDForOffEnvironmentStart(params jobs.StartEnvironmentJobParams) string {
	if params.StartedByJobID != "" {
		return params.StartedByJobID
	}
	return sharedpkg.CurrentEnvironmentJobID()
}

func startCommandJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.StartEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "name", params.Name)
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "command", params.Command)
	shared.PutEnvironmentToolArgument(arguments, "dir", params.Dir)
	shared.PutEnvironmentToolArgument(arguments, "env", params.Env)
	shared.PutEnvironmentToolArgument(arguments, "maxOutputBytes", params.MaxOutputBytes)
	shared.PutEnvironmentToolArgument(arguments, "leaseTtlSeconds", shared.LeaseTTLSeconds(params.LeaseTTL))
	shared.PutEnvironmentToolArgument(arguments, "handoff", params.Handoff)
	shared.PutEnvironmentToolArgument(arguments, "exclusive", params.Exclusive)
	shared.PutEnvironmentToolArgument(arguments, "startedByJobId", startedByJobIDForOffEnvironmentStart(params))
	arguments["wait"] = false
	started, resolved, err := shared.CallEnvironmentTool[environmentJobEnvelopeResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_raw", arguments, false)
	if err != nil {
		return sharedpkg.EnvironmentJob{}, resolved, jobs.DescribeExclusiveJobStartVersionSkew(params.Tenant, params.Environment, params.Exclusive, err)
	}
	if !resolved {
		return sharedpkg.EnvironmentJob{}, resolved, nil
	}
	if started.JobID == "" {
		return sharedpkg.EnvironmentJob{}, resolved, fmt.Errorf("%s/%s started the job but returned no jobId", params.Tenant, params.Environment)
	}
	status, resolved, err := jobStatusFromEnvironment(ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, started.JobID)
	if err != nil || !resolved {
		return sharedpkg.EnvironmentJob{}, resolved, err
	}
	if status.Job == nil {
		return sharedpkg.EnvironmentJob{}, resolved, fmt.Errorf("%s/%s returned no job for id %q", params.Tenant, params.Environment, started.JobID)
	}
	return *status.Job, resolved, nil
}

func attachJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.AttachEnvironmentJobParams) (sharedpkg.EnvironmentJob, bool, error) {
	if err := jobs.ValidateEnvironmentJobAttach(params); err != nil {
		return sharedpkg.EnvironmentJob{}, false, err
	}
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "name", params.Name)
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "pid", params.PID)
	shared.PutEnvironmentToolArgument(arguments, "logPath", params.LogPath)
	shared.PutEnvironmentToolArgument(arguments, "leaseTtlSeconds", shared.LeaseTTLSeconds(params.LeaseTTL))
	result, resolved, err := shared.CallEnvironmentTool[environmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_attach", arguments, false)
	return result.Job, resolved, err
}

func jobStatusFromEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, tenant, environment, id string) (environmentJobStatusResult, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "id", id)
	return shared.CallEnvironmentTool[environmentJobStatusResult](ctx, commandCtx, resolveOpen, tenant, environment, "exec_job_status", arguments, true)
}

func awaitJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.AwaitEnvironmentJobParams) (jobs.AwaitEnvironmentJobResult, bool, error) {
	if err := jobs.ValidateEnvironmentJobAwaitTimeout(params.Timeout); err != nil {
		return jobs.AwaitEnvironmentJobResult{}, false, err
	}
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "timeoutSeconds", int64(params.Timeout.Seconds()))
	return shared.CallEnvironmentTool[jobs.AwaitEnvironmentJobResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_await", arguments, true)
}

func jobOutputFromEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.ReadEnvironmentJobOutputParams) (jobs.EnvironmentJobOutput, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "offset", params.Offset)
	shared.PutEnvironmentToolArgument(arguments, "maxBytes", params.MaxBytes)
	return shared.CallEnvironmentTool[jobs.EnvironmentJobOutput](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_output", arguments, true)
}

func cancelJobInEnvironment(ctx context.Context, commandCtx core.Context, resolveOpen shared.OpenResolver, params jobs.CancelEnvironmentJobParams) (jobs.CancelEnvironmentJobResult, bool, error) {
	arguments := map[string]any{}
	shared.PutEnvironmentToolArgument(arguments, "id", params.ID)
	shared.PutEnvironmentToolArgument(arguments, "signal", params.Signal)
	result, resolved, err := shared.CallEnvironmentTool[environmentJobCancelResult](ctx, commandCtx, resolveOpen, params.Tenant, params.Environment, "exec_job_cancel", arguments, false)
	return result.Cancel, resolved, err
}
