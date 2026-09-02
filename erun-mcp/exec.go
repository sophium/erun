package erunmcp

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// RawInput absorbs job_start's command mode (#1246): dir, env, and a
// background job in addition to the plain foreground run exec_raw always
// supported. Its agent mode became the separate exec_agent tool instead,
// since a streaming AI-tool run has nothing in common with exec_raw's
// argv-and-captured-output shape beyond both starting a job.
type RawInput struct {
	Command []string          `json:"command" jsonschema:"command and arguments to execute from the runtime repo root"`
	Stdin   string            `json:"stdin,omitempty" jsonschema:"optional stdin to pass to the command; only used in the foreground (wait true)"`
	Dir     string            `json:"dir,omitempty" jsonschema:"working directory to run from, absolute or relative to the runtime repo root; defaults to the runtime repo root"`
	Env     map[string]string `json:"env,omitempty" jsonschema:"additional KEY=VALUE environment for the command, on top of what it inherits from the runtime pod; only valid with wait false, since a foreground call already runs in this process's own environment with nothing to extend it with. Values land in the job supervisor's argv, visible to anything that can list processes in this environment, so this is not where secrets belong; PATH, LD_PRELOAD, and a few other names that could redirect what the job executes are refused, as is any ERUN_ name"`
	// Tenant/Environment must match this server's own scope in both modes (see
	// resolveLocalTarget/scopedTenantEnv): in the foreground there is no handle
	// to address, so a value here is validated but otherwise unused, this
	// server's own context already applies; a mismatch is refused rather than
	// silently run locally anyway, which is what let a caller believe a
	// foreground exec_raw had reached a different environment when it had not.
	// Name/ID address a backgrounded command the way job_start's did.
	Tenant          string `json:"tenant,omitempty" jsonschema:"tenant whose environment runs the command; defaults to the server tenant context, and must match it: this server only acts on its own environment. In the foreground (wait true) this is validated but otherwise unused, since the command always runs in this process"`
	Environment     string `json:"environment,omitempty" jsonschema:"environment to run the command in; defaults to the server environment context, and must match it: this server only acts on its own environment. In the foreground (wait true) this is validated but otherwise unused, since the command always runs in this process"`
	Name            string `json:"name,omitempty" jsonschema:"what the backgrounded command is, shown wherever the environment reports as busy; only used with wait false. Defaults to exec_raw"`
	ID              string `json:"id,omitempty" jsonschema:"handle to address the backgrounded command by; only used with wait false. Defaults to name, so re-running the same named command keeps one stable handle"`
	MaxOutputBytes  int64  `json:"maxOutputBytes,omitempty" jsonschema:"cap on captured output in bytes for a backgrounded command; past it output is dropped and the job reports outputTruncated. Only used with wait false. Defaults to 16777216"`
	LeaseTTLSeconds int64  `json:"leaseTtlSeconds,omitempty" jsonschema:"activity lease TTL a backgrounded command renews inside while it runs; only used with wait false. Defaults to 900"`
	Handoff         bool   `json:"handoff,omitempty" jsonschema:"mark the backgrounded command as deliberately meant to outlive whatever starts it; only used with wait false. When this call itself runs from inside another job's own work, that job otherwise waits for this one to reach a verdict before reporting its own outcome; set true for work meant to keep running past the caller's own turn on purpose"`
	StartedByJobID  string `json:"startedByJobId,omitempty" jsonschema:"internal: the job this call is being made on behalf of, so that job's own finish check can find this one as work it started; only used with wait false. Only needed when this call itself runs from inside another job's own work AND reaches this tool through this environment's MCP edge rather than as a plain nested subprocess -- a nested subprocess (an agent's own Bash tool calling the erun CLI directly) already gets this for free from its own ERUN_JOB_ID and never needs to set it"`
	Preview         bool   `json:"preview,omitempty" jsonschema:"when true, trace the command (or, with wait false, the job that would start) without executing it"`
	Verbosity       int    `json:"verbosity,omitempty" jsonschema:"feedback level matching CLI -v semantics"`
	Wait            *bool  `json:"wait,omitempty" jsonschema:"when true (the default), run in the foreground and return once the command exits, with its captured stdout/stderr inline -- today's behaviour. Set false to detach the command as a background job instead and get back {jobId, state: running} immediately: erun gives it its own session, captures merged stdout/stderr to the job's log, and records the exit status by waiting on the process, so nothing has to be wrapped in setsid/nohup/a redirect. Poll exec_job_status/exec_job_await/exec_job_output for the outcome. This is the replacement for the removed job_start tool's command mode; reach for exec_agent instead when the work is an AI tool rather than a plain command"`
}

func rawTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, RawInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input RawInput) (*mcp.CallToolResult, JobEnvelopeOutput, error) {
		wait := waitRequested(input.Wait)
		if wait && len(input.Env) > 0 {
			return nil, JobEnvelopeOutput{}, fmt.Errorf("env only applies to a backgrounded command: set wait to false, or drop env and set the process's environment before calling exec_raw")
		}
		if !wait {
			output, err := execRawBackground(runtime, input)
			return nil, output, err
		}
		output, err := execRawForeground(runtime, input)
		return nil, output, err
	}
}

func execRawForeground(runtime RuntimeConfig, input RawInput) (JobEnvelopeOutput, error) {
	if _, _, err := scopedTenantEnv(input.Tenant, input.Environment, runtime); err != nil {
		return JobEnvelopeOutput{}, err
	}
	traceOutput := new(bytes.Buffer)
	ctx := runtimeCallContext(input.Preview, input.Verbosity, strings.NewReader(input.Stdin), traceOutput, traceOutput)

	workDir, err := execRawResolveDir(runtime, input.Dir)
	if err != nil {
		return JobEnvelopeOutput{}, err
	}

	output, err := runCommandOutput(ctx, workDir, traceOutput, nil, func(runCtx eruncommon.Context) error {
		return eruncommon.RunRawCommand(runCtx, eruncommon.RawCommandSpec{
			Dir:  workDir,
			Args: input.Command,
		}, nil)
	})
	return JobEnvelopeOutput{CommandOutput: output, Wait: true}, err
}

// execRawBackground detaches the command as a job exactly the way job_start's
// command mode did, since exec_raw already runs a subprocess -- this reuses
// that engine rather than the in-process task job the rest of the
// job-envelope surface uses, which has no subprocess to detach.
func execRawBackground(runtime RuntimeConfig, input RawInput) (JobEnvelopeOutput, error) {
	tenant, environment, err := resolveLocalTarget(runtime, input.Tenant, input.Environment)
	if err != nil {
		return JobEnvelopeOutput{}, err
	}
	dir, err := execRawResolveDir(runtime, input.Dir)
	if err != nil {
		return JobEnvelopeOutput{}, err
	}
	supervisor, err := eruncommon.ResolveErunExecutable()
	if err != nil {
		return JobEnvelopeOutput{}, err
	}

	trace := new(bytes.Buffer)
	ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, trace, trace)
	job, err := eruncommon.StartEnvironmentJob(ctx, eruncommon.StartEnvironmentJobParams{
		Tenant:         tenant,
		Environment:    environment,
		Name:           firstNonEmpty(strings.TrimSpace(input.Name), "exec_raw"),
		ID:             strings.TrimSpace(input.ID),
		Command:        input.Command,
		Dir:            dir,
		Env:            input.Env,
		MaxOutputBytes: input.MaxOutputBytes,
		LeaseTTL:       time.Duration(input.LeaseTTLSeconds) * time.Second,
		Handoff:        input.Handoff,
		StartedByJobID: input.StartedByJobID,
		SupervisorPath: supervisor,
	})
	if err != nil {
		return JobEnvelopeOutput{}, err
	}
	return JobEnvelopeOutput{
		CommandOutput: CommandOutput{
			Executed: !input.Preview,
			Trace:    normalizeTraceLines(trace.String()),
		},
		JobID: job.ID,
		State: job.State,
		Wait:  false,
	}, nil
}

func execRawResolveDir(runtime RuntimeConfig, dir string) (string, error) {
	repoPath, err := runtimeRepoPath(runtime.Context)
	if err != nil {
		return "", err
	}
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		return repoPath, nil
	}
	if filepath.IsAbs(trimmed) {
		return trimmed, nil
	}
	return filepath.Join(repoPath, trimmed), nil
}
