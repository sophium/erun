package jobs

import (
	"bytes"
	"context"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/base/model"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobresult"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// JobAttachInput registers work the caller started another way.
type JobAttachInput struct {
	Tenant          string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the work; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment     string `json:"environment,omitempty" jsonschema:"environment holding the work; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Name            string `json:"name" jsonschema:"what the work is; shown wherever the environment reports as busy"`
	ID              string `json:"id,omitempty" jsonschema:"handle to address the job by; defaults to the name. Re-attaching the same id renews the lease"`
	PID             int    `json:"pid" jsonschema:"process to track; the job resolves against this pid and nothing else, and reports unknown once it is gone because erun never waited on it to observe an exit status"`
	LogPath         string `json:"logPath,omitempty" jsonschema:"file the work already writes its output to, so job_output can serve it"`
	LeaseTTLSeconds int64  `json:"leaseTtlSeconds,omitempty" jsonschema:"activity lease TTL; re-attach to renew. Defaults to 900"`
	Preview         bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the attach without recording it"`
}

func JobAttachTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobAttachInput) (*mcp.CallToolResult, jobresult.JobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobAttachInput) (*mcp.CallToolResult, jobresult.JobResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobresult.JobResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := toolio.RuntimeCallContext(input.Preview, 0, nil, trace, trace)
		job, err := jobspkg.AttachEnvironmentJob(ctx, model.AttachEnvironmentJobParams{
			Tenant:      tenant,
			Environment: environment,
			Name:        input.Name,
			ID:          input.ID,
			PID:         input.PID,
			LogPath:     input.LogPath,
			LeaseTTL:    time.Duration(input.LeaseTTLSeconds) * time.Second,
		})
		if err != nil {
			return nil, jobresult.JobResult{}, err
		}
		return nil, jobresult.JobResult{
			Tenant:      tenant,
			Environment: environment,
			Job:         job,
			Trace:       toolio.NormalizeTraceLines(trace.String()),
			Executed:    !input.Preview,
		}, nil
	}
}

// jobCommandPreviewChars bounds the echoed command; an agent job's argv carries
// its whole prompt.
const jobCommandPreviewChars = 240

// previewJobForStatus returns a truncated copy, never mutating job.
func previewJobForStatus(job jobstate.EnvironmentJob) jobstate.EnvironmentJob {
	if preview, truncated := previewCommand(job.Command); truncated {
		job.Command = preview
	}
	return job
}

func previewCommand(command []string) ([]string, bool) {
	joined := strings.Join(command, " ")
	if utf8.RuneCountInString(joined) <= jobCommandPreviewChars {
		return command, false
	}
	runes := []rune(joined)
	return []string{strings.TrimSpace(string(runes[:jobCommandPreviewChars])) + "…"}, true
}

// JobStatusInput selects one job or every retained job.
type JobStatusInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment to query; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment to query; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ID          string `json:"id,omitempty" jsonschema:"job to report; omit to return every retained job, newest first"`
}

// JobStatusResult never reports an unrecorded outcome; a vanished supervisor
// reads as unknown. Jobs is empty when id selects one job.
type JobStatusResult struct {
	Tenant      string                    `json:"tenant"`
	Environment string                    `json:"environment"`
	Job         *jobstate.EnvironmentJob  `json:"job,omitempty"`
	Jobs        []jobstate.EnvironmentJob `json:"jobs"`
}

func JobStatusTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobStatusInput) (*mcp.CallToolResult, JobStatusResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobStatusInput) (*mcp.CallToolResult, JobStatusResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobStatusResult{}, err
		}
		result := JobStatusResult{Tenant: tenant, Environment: environment, Jobs: []jobstate.EnvironmentJob{}}
		now := time.Now()
		if strings.TrimSpace(input.ID) != "" {
			job, err := jobspkg.LoadEnvironmentJob(tenant, environment, input.ID, now)
			if err != nil {
				return nil, JobStatusResult{}, err
			}
			preview := previewJobForStatus(job)
			result.Job = &preview
			return nil, result, nil
		}
		jobs, err := jobspkg.LoadEnvironmentJobs(tenant, environment, now)
		if err != nil {
			return nil, JobStatusResult{}, err
		}
		for _, job := range jobs {
			result.Jobs = append(result.Jobs, previewJobForStatus(job))
		}
		return nil, result, nil
	}
}

// JobAwaitInput bounds one wait.
type JobAwaitInput struct {
	Tenant         string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment    string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ID             string `json:"id" jsonschema:"job to wait for"`
	TimeoutSeconds int64  `json:"timeoutSeconds,omitempty" jsonschema:"how long to wait before returning still-running; defaults to 30 and may not exceed 600 (a larger value is refused, not capped silently), so no call is ever held open for the work's lifetime. For a job that can run longer than 600s, call this tool again each time it reports timedOut=true rather than requesting one longer wait"`
}

func JobAwaitTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobAwaitInput) (*mcp.CallToolResult, jobspkg.AwaitEnvironmentJobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobAwaitInput) (*mcp.CallToolResult, jobspkg.AwaitEnvironmentJobResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobspkg.AwaitEnvironmentJobResult{}, err
		}
		result, err := jobspkg.AwaitEnvironmentJob(model.AwaitEnvironmentJobParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          input.ID,
			Timeout:     time.Duration(input.TimeoutSeconds) * time.Second,
		})
		if err != nil {
			return nil, jobspkg.AwaitEnvironmentJobResult{}, err
		}
		result.Job = previewJobForStatus(result.Job)
		return nil, result, nil
	}
}

// JobOutputInput pages through a job's captured output.
type JobOutputInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ID          string `json:"id" jsonschema:"job whose output to read"`
	Offset      int64  `json:"offset,omitempty" jsonschema:"byte offset to read from; pass back the previous read's nextOffset so a poll continues rather than repeats"`
	MaxBytes    int64  `json:"maxBytes,omitempty" jsonschema:"most bytes to return in this read; defaults to 65536"`
}

func JobOutputTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobOutputInput) (*mcp.CallToolResult, jobspkg.EnvironmentJobOutput, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobOutputInput) (*mcp.CallToolResult, jobspkg.EnvironmentJobOutput, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobspkg.EnvironmentJobOutput{}, err
		}
		output, err := jobspkg.ReadEnvironmentJobOutput(model.ReadEnvironmentJobOutputParams{
			Tenant:      tenant,
			Environment: environment,
			ID:          input.ID,
			Offset:      input.Offset,
			MaxBytes:    input.MaxBytes,
		})
		if err != nil {
			return nil, jobspkg.EnvironmentJobOutput{}, err
		}
		return nil, output, nil
	}
}

// JobCancelInput selects the job to signal.
type JobCancelInput struct {
	Tenant      string `json:"tenant,omitempty" jsonschema:"tenant whose environment holds the job; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment string `json:"environment,omitempty" jsonschema:"environment holding the job; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	ID          string `json:"id" jsonschema:"job to cancel"`
	Signal      string `json:"signal,omitempty" jsonschema:"signal to send: TERM (default), INT, HUP, or KILL"`
	Preview     bool   `json:"preview,omitempty" jsonschema:"when true, resolve and trace the target without signalling it"`
}

// JobCancelResult reports what the cancel did.
type JobCancelResult struct {
	Tenant      string                             `json:"tenant"`
	Environment string                             `json:"environment"`
	Cancel      jobspkg.CancelEnvironmentJobResult `json:"cancel"`
	Trace       []string                           `json:"trace,omitempty"`
}

func JobCancelTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobCancelInput) (*mcp.CallToolResult, JobCancelResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobCancelInput) (*mcp.CallToolResult, JobCancelResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, JobCancelResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := toolio.RuntimeCallContext(input.Preview, 0, nil, trace, trace)
		cancel, err := jobspkg.CancelEnvironmentJob(ctx, model.CancelEnvironmentJobParams{
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
			Trace:       toolio.NormalizeTraceLines(trace.String()),
		}, nil
	}
}
