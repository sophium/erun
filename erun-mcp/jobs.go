package erunmcp

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	eruncommon "github.com/sophium/erun/erun-common"
)

// jobs.go mirrors gate_runs.go's platform-call shape for the jobs surface:
// the platform's record of work in flight, so an in-pod agent records what it
// is doing without shelling out, and a second agent asking for the same scope
// is told who already holds it.

// PlatformJobResult is one platform job's tool result. It is named apart
// from job.go's JobResult, which is an environment-scoped in-pod job.
type PlatformJobResult struct {
	Preview bool                   `json:"preview"`
	Job     eruncommon.PlatformJob `json:"job,omitempty"`
	Trace   []string               `json:"trace,omitempty"`
}

// PlatformJobListResult is a platform job listing's tool result.
type PlatformJobListResult struct {
	Preview bool                     `json:"preview"`
	Jobs    []eruncommon.PlatformJob `json:"jobs,omitempty"`
	Trace   []string                 `json:"trace,omitempty"`
}

type JobsListInput struct {
	platformAliasInput
	Status        string `json:"status,omitempty" jsonschema:"filter by status: RUNNING, SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED"`
	EnvironmentID string `json:"environmentId,omitempty" jsonschema:"filter by the platform's environment id"`
	IssueRef      string `json:"issueRef,omitempty" jsonschema:"filter by the issue the work belongs to, in owner/repo#number form"`
	Scope         string `json:"scope,omitempty" jsonschema:"filter by the scope a job claims"`
	ActorID       string `json:"actorId,omitempty" jsonschema:"filter by the actor holding the job"`
}

func jobsListTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobsListInput) (*mcp.CallToolResult, PlatformJobListResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobsListInput) (*mcp.CallToolResult, PlatformJobListResult, error) {
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "jobs_list"
		jobs, err := eruncommon.RunJobList(ctx, runtime.Store, input.Alias, eruncommon.JobListParams{
			Status:        input.Status,
			EnvironmentID: input.EnvironmentID,
			IssueRef:      input.IssueRef,
			Scope:         input.Scope,
			ActorID:       input.ActorID,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformJobListResult{}, err
		}
		return nil, PlatformJobListResult{Preview: input.Preview, Jobs: jobs, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type JobsShowInput struct {
	platformAliasInput
	JobID string `json:"jobId" jsonschema:"job id to show"`
}

func jobsShowTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobsShowInput) (*mcp.CallToolResult, PlatformJobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobsShowInput) (*mcp.CallToolResult, PlatformJobResult, error) {
		if strings.TrimSpace(input.JobID) == "" {
			return nil, PlatformJobResult{}, fmt.Errorf("jobId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "jobs_show"
		job, err := eruncommon.RunJobShow(ctx, runtime.Store, input.Alias, input.JobID, cloudDependencies())
		if err != nil {
			return nil, PlatformJobResult{}, err
		}
		return nil, PlatformJobResult{Preview: input.Preview, Job: job, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type JobsStartInput struct {
	platformAliasInput
	JobType     string `json:"jobType" jsonschema:"type of work: fix, review, gate, release, deploy, investigate, plan, triage, or maintenance"`
	Summary     string `json:"summary" jsonschema:"what the work is, in prose — never the command that performs it"`
	ActorID     string `json:"actorId" jsonschema:"who is doing the work: the orchestrator id or agent identity"`
	ActorKind   string `json:"actorKind,omitempty" jsonschema:"what kind of actor this is: agent (default), orchestrator, or human"`
	Environment string `json:"environment,omitempty" jsonschema:"local environment name this work runs in; omit for host-side work"`
	IssueRef    string `json:"issueRef,omitempty" jsonschema:"the issue this work belongs to, in owner/repo#number form"`
	Scope       string `json:"scope,omitempty" jsonschema:"what this job claims, so a second actor asking for the same scope is told who holds it"`
	LocalJobID  string `json:"localJobId,omitempty" jsonschema:"the in-pod job id this mirrors, when there is one"`
}

func jobsStartTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobsStartInput) (*mcp.CallToolResult, PlatformJobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobsStartInput) (*mcp.CallToolResult, PlatformJobResult, error) {
		actorKind := input.ActorKind
		if strings.TrimSpace(actorKind) == "" {
			// The overwhelming majority of callers are agents recording their
			// own work; an orchestrator or a human says so explicitly.
			actorKind = "agent"
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "jobs_start"
		job, err := eruncommon.RunJobClaim(ctx, runtime.Store, input.Alias, eruncommon.JobClaimParams{
			Environment: input.Environment,
			JobType:     input.JobType,
			IssueRef:    input.IssueRef,
			Summary:     input.Summary,
			ActorKind:   actorKind,
			ActorID:     input.ActorID,
			Scope:       input.Scope,
			LocalJobID:  input.LocalJobID,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformJobResult{}, err
		}
		return nil, PlatformJobResult{Preview: input.Preview, Job: job, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}

type JobsFinishInput struct {
	platformAliasInput
	JobID      string `json:"jobId" jsonschema:"job id to update"`
	Status     string `json:"status,omitempty" jsonschema:"close the job as SUCCEEDED, FAILED, ABANDONED, or SUPERSEDED"`
	Summary    string `json:"summary,omitempty" jsonschema:"refresh what the job is doing, in prose"`
	LocalJobID string `json:"localJobId,omitempty" jsonschema:"record the in-pod job id this mirrors"`
}

func jobsFinishTool(runtime RuntimeConfig) func(context.Context, *mcp.CallToolRequest, JobsFinishInput) (*mcp.CallToolResult, PlatformJobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input JobsFinishInput) (*mcp.CallToolResult, PlatformJobResult, error) {
		if strings.TrimSpace(input.JobID) == "" {
			return nil, PlatformJobResult{}, fmt.Errorf("jobId is required")
		}
		traceOutput := strings.Builder{}
		ctx := runtimeCallContext(input.Preview, input.Verbosity, nil, &traceOutput, &traceOutput)
		ctx.MCPTool = "jobs_finish"
		job, err := eruncommon.RunJobUpdate(ctx, runtime.Store, input.Alias, eruncommon.JobUpdateParams{
			JobID:      input.JobID,
			Status:     input.Status,
			Summary:    input.Summary,
			LocalJobID: input.LocalJobID,
		}, cloudDependencies())
		if err != nil {
			return nil, PlatformJobResult{}, err
		}
		return nil, PlatformJobResult{Preview: input.Preview, Job: job, Trace: normalizeTraceLines(traceOutput.String())}, nil
	}
}
