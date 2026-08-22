package erunmcp

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// The job tools are the host-side half of the job surface. An orchestrator
// driving an environment from outside starts long work here, then comes back for
// its state — instead of using `raw` to hand-roll detachment, a log redirect, a
// polling loop, a sentinel token, and a parse of this envelope to recover an
// exit code that a wrapping shell had already mangled.

// JobStartInput is the work to detach in the environment.
type JobStartInput struct {
	Tenant          string            `json:"tenant,omitempty" jsonschema:"tenant whose environment runs the job; defaults to the server tenant context"`
	Environment     string            `json:"environment,omitempty" jsonschema:"environment to run the job in; defaults to the server environment context"`
	Name            string            `json:"name" jsonschema:"what the work is; shown wherever the environment reports as busy"`
	ID              string            `json:"id,omitempty" jsonschema:"handle to address the job by; defaults to the name, so re-running the same named work keeps one stable handle"`
	Command         []string          `json:"command,omitempty" jsonschema:"command and arguments to run, as an argv array; pass [\"sh\",\"-c\",\"...\"] only when shell features are genuinely needed. Omit when running an agent"`
	Agent           string            `json:"agent,omitempty" jsonschema:"run an AI tool instead of a command: claude or codex. erun invokes it in its streaming mode, so job_output returns events while the agent works and job_status reports its current activity rather than only running. Requires prompt and excludes command"`
	Prompt          string            `json:"prompt,omitempty" jsonschema:"what the agent should do; only valid with agent"`
	Dir             string            `json:"dir,omitempty" jsonschema:"working directory to run from; defaults to the runtime repo root"`
	Env             map[string]string `json:"env,omitempty" jsonschema:"additional KEY=VALUE environment for the job's own process, on top of what it inherits from the runtime pod — e.g. raising CLAUDE_CODE_MAX_OUTPUT_TOKENS for one agent run. Values land in the job supervisor's argv, visible to anything that can list processes in this environment, so this is not where secrets belong. PATH, LD_PRELOAD, and a few other names that could redirect what the job executes are refused, as is any ERUN_ name"`
	MaxOutputBytes  int64             `json:"maxOutputBytes,omitempty" jsonschema:"cap on captured output in bytes; past it output is dropped and the job reports outputTruncated. Does not affect an agent job's progress, which is folded from the tool's stream directly and keeps updating past the cap. Defaults to 16777216"`
	LeaseTTLSeconds int64             `json:"leaseTtlSeconds,omitempty" jsonschema:"activity lease TTL the job renews inside while it runs; defaults to 900"`
	Preview         bool              `json:"preview,omitempty" jsonschema:"when true, resolve and trace the job without starting it"`
}

// JobResult is the handle plus whatever is known about the job right now.
type JobResult struct {
	Tenant      string                    `json:"tenant"`
	Environment string                    `json:"environment"`
	Job         eruncommon.EnvironmentJob `json:"job"`
	Trace       []string                  `json:"trace,omitempty"`
	Executed    bool                      `json:"executed"`
}

func jobStartTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobStartInput) (*mcp.CallToolResult, JobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobStartInput) (*mcp.CallToolResult, JobResult, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobResult{}, err
		}
		dir := strings.TrimSpace(input.Dir)
		if dir == "" {
			if dir, err = runtimeRepoPath(runtime.Context); err != nil {
				return nil, JobResult{}, err
			}
		}
		// The MCP server is not itself the erun binary, so the supervisor is the
		// environment's own erun executable; a missing one fails here rather than
		// producing a handle to nothing.
		supervisor, err := eruncommon.ResolveErunExecutable()
		if err != nil {
			return nil, JobResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, 0, nil, trace, trace)
		job, err := eruncommon.StartEnvironmentJob(ctx, eruncommon.StartEnvironmentJobParams{
			Tenant:         tenant,
			Environment:    environment,
			Name:           input.Name,
			ID:             input.ID,
			Command:        input.Command,
			Agent:          input.Agent,
			Prompt:         input.Prompt,
			Dir:            dir,
			Env:            input.Env,
			MaxOutputBytes: input.MaxOutputBytes,
			LeaseTTL:       time.Duration(input.LeaseTTLSeconds) * time.Second,
			SupervisorPath: supervisor,
		})
		if err != nil {
			return nil, JobResult{}, err
		}
		return nil, JobResult{
			Tenant:      tenant,
			Environment: environment,
			Job:         job,
			Trace:       normalizeTraceLines(trace.String()),
			Executed:    !input.Preview,
		}, nil
	}
}

// JobAttachInput registers work the caller started another way.
type JobAttachInput struct {
	Tenant          string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the work; defaults to the server tenant context"`
	Environment     string `json:"environment,omitempty" jsonschema:"environment holding the work; defaults to the server environment context"`
	Name            string `json:"name" jsonschema:"what the work is; shown wherever the environment reports as busy"`
	ID              string `json:"id,omitempty" jsonschema:"handle to address the job by; defaults to the name. Re-attaching the same id renews the lease"`
	PID             int    `json:"pid" jsonschema:"process to track; the job resolves against this pid and nothing else, and reports unknown once it is gone because erun never waited on it to observe an exit status"`
	LogPath         string `json:"logPath,omitempty" jsonschema:"file the work already writes its output to, so job_output can serve it"`
	LeaseTTLSeconds int64  `json:"leaseTtlSeconds,omitempty" jsonschema:"activity lease TTL; re-attach to renew. Defaults to 900"`
	Preview         bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the attach without recording it"`
}

func jobAttachTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobAttachInput) (*mcp.CallToolResult, JobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobAttachInput) (*mcp.CallToolResult, JobResult, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, 0, nil, trace, trace)
		job, err := eruncommon.AttachEnvironmentJob(ctx, eruncommon.AttachEnvironmentJobParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        input.Name,
			ID:          input.ID,
			PID:         input.PID,
			LogPath:     input.LogPath,
			LeaseTTL:    time.Duration(input.LeaseTTLSeconds) * time.Second,
		})
		if err != nil {
			return nil, JobResult{}, err
		}
		return nil, JobResult{
			Tenant:      tenant,
			Environment: environment,
			Job:         job,
			Trace:       normalizeTraceLines(trace.String()),
			Executed:    !input.Preview,
		}, nil
	}
}

// JobStatusInput selects one job or every retained job.
type JobStatusInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to query; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to query; defaults to the server environment context"`
	ID          string `json:"id,omitempty" jsonschema:"job to report; omit to return every retained job, newest first"`
}

// JobStatusResult is always a definite answer. A job whose supervisor vanished
// reports state unknown with a reason; it never reports an outcome nobody
// recorded, and the list is never silently shortened.
type JobStatusResult struct {
	Tenant      string                      `json:"tenant"`
	Environment string                      `json:"environment"`
	Job         *eruncommon.EnvironmentJob  `json:"job,omitempty"`
	Jobs        []eruncommon.EnvironmentJob `json:"jobs"`
}

func jobStatusTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobStatusInput) (*mcp.CallToolResult, JobStatusResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobStatusInput) (*mcp.CallToolResult, JobStatusResult, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobStatusResult{}, err
		}
		result := JobStatusResult{Tenant: tenant, Environment: environment, Jobs: []eruncommon.EnvironmentJob{}}
		now := time.Now()
		if strings.TrimSpace(input.ID) != "" {
			job, err := eruncommon.LoadEnvironmentJob(tenant, environment, input.ID, now)
			if err != nil {
				return nil, JobStatusResult{}, err
			}
			result.Job = &job
			result.Jobs = append(result.Jobs, job)
			return nil, result, nil
		}
		jobs, err := eruncommon.LoadEnvironmentJobs(tenant, environment, now)
		if err != nil {
			return nil, JobStatusResult{}, err
		}
		if jobs != nil {
			result.Jobs = jobs
		}
		return nil, result, nil
	}
}

// JobAwaitInput bounds one wait.
type JobAwaitInput struct {
	Tenant         string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context"`
	Environment    string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context"`
	ID             string `json:"id" jsonschema:"job to wait for"`
	TimeoutSeconds int64  `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before returning still-running; defaults to 30 and may not exceed 600, so no call is ever held open for the work's lifetime"`
}

func jobAwaitTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobAwaitInput) (*mcp.CallToolResult, eruncommon.AwaitEnvironmentJobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobAwaitInput) (*mcp.CallToolResult, eruncommon.AwaitEnvironmentJobResult, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, eruncommon.AwaitEnvironmentJobResult{}, err
		}
		result, err := eruncommon.AwaitEnvironmentJob(eruncommon.AwaitEnvironmentJobParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          input.ID,
			Timeout:     time.Duration(input.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, eruncommon.AwaitEnvironmentJobResult{}, err
		}
		return nil, result, nil
	}
}

// JobOutputInput pages through a job's captured output.
type JobOutputInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context"`
	ID          string `json:"id" jsonschema:"job whose output to read"`
	Offset      int64  `json:"offset,omitempty" jsonschema:"byte offset to read from; pass back the previous read's nextOffset so a poll continues rather than repeats"`
	MaxBytes    int64  `json:"maxBytes,omitempty" jsonschema:"most bytes to return in this read; defaults to 65536"`
}

func jobOutputTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobOutputInput) (*mcp.CallToolResult, eruncommon.EnvironmentJobOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobOutputInput) (*mcp.CallToolResult, eruncommon.EnvironmentJobOutput, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, eruncommon.EnvironmentJobOutput{}, err
		}
		output, err := eruncommon.ReadEnvironmentJobOutput(eruncommon.ReadEnvironmentJobOutputParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          input.ID,
			Offset:      input.Offset,
			MaxBytes:    input.MaxBytes,
		})
		if err != nil {
			return nil, eruncommon.EnvironmentJobOutput{}, err
		}
		return nil, output, nil
	}
}

// JobCancelInput selects the job to signal.
type JobCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context"`
	Environment string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context"`
	ID          string `json:"id" jsonschema:"job to cancel"`
	Signal      string `json:"signal,omitempty" jsonschema:"signal to send: TERM (default), INT, HUP, or KILL"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the target without signalling it"`
}

// JobCancelResult reports what the cancel did.
type JobCancelResult struct {
	Tenant      string                                `json:"tenant"`
	Environment string                                `json:"environment"`
	Cancel      eruncommon.CancelEnvironmentJobResult `json:"cancel"`
	Trace       []string                              `json:"trace,omitempty"`
}

func jobCancelTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobCancelInput) (*mcp.CallToolResult, JobCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobCancelInput) (*mcp.CallToolResult, JobCancelResult, error) {
		tenant, environment, err := resolveJobTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobCancelResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := runtimeCallContext(input.Preview, 0, nil, trace, trace)
		cancel, err := eruncommon.CancelEnvironmentJob(ctx, eruncommon.CancelEnvironmentJobParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          input.ID,
			Signal:      input.Signal,
		})
		if err != nil {
			return nil, JobCancelResult{}, err
		}
		return nil, JobCancelResult{
			Tenant:      tenant,
			Environment: environment,
			Cancel:      cancel,
			Trace:       normalizeTraceLines(trace.String()),
		}, nil
	}
}

func resolveJobTarget(runtime RuntimeConfig, tenant, environment string) (string, string, error) {
	resolvedTenant := firstNonEmpty(tenant, runtime.Context.Tenant)
	resolvedEnvironment := firstNonEmpty(environment, runtime.Context.Environment)
	if strings.TrimSpace(resolvedTenant) == "" || strings.TrimSpace(resolvedEnvironment) == "" {
		return "", "", fmt.Errorf("tenant and environment are required")
	}
	return resolvedTenant, resolvedEnvironment, nil
}
