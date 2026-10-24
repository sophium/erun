package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// A remote-agent/runtime env's jobs run in its pod, so reads go through its MCP
// edge when reachable. The local store answers when the env is not open (it also
// holds the desktop's own Investigate job); a stale forward is reported as
// unreachable, never as empty.

// LoadEnvironmentJobs lists an environment's retained jobs, newest first.
func (a *App) LoadEnvironmentJobs(tenant, environment string) ([]uiEnvironmentJob, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := errMissingTenantOrEnvironment("load environment jobs", tenant, environment); err != nil {
		return nil, err
	}
	result, useMCP, err := a.jobsReachability(tenant, environment)
	if err != nil {
		return nil, err
	}
	if !useMCP {
		jobs, err := jobspkg.LoadEnvironmentJobs(tenant, environment, time.Now())
		if err != nil {
			return nil, err
		}
		return environmentJobsToUI(jobs), nil
	}
	jobs, err := a.deps.loadEnvironmentJobs(a.backgroundContext(), mcpEndpointForOpenResult(result), a.mcpBearer(tenant, environment))
	if err != nil {
		return nil, a.wrapJobMCPErr(result, err)
	}
	return environmentJobsToUI(jobs), nil
}

// ReadEnvironmentJobOutput returns one page of a job's captured output.
func (a *App) ReadEnvironmentJobOutput(input uiJobOutputInput) (uiEnvironmentJobOutput, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	id := strings.TrimSpace(input.ID)
	if tenant == "" || environment == "" || id == "" {
		return uiEnvironmentJobOutput{}, fmt.Errorf("tenant, environment, and job id are required")
	}
	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultJobOutputPageBytes
	}
	params := jobspkg.ReadEnvironmentJobOutputParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Offset:      input.Offset,
		MaxBytes:    maxBytes,
	}
	result, useMCP, err := a.jobsReachability(tenant, environment)
	if err != nil {
		return uiEnvironmentJobOutput{}, err
	}
	if !useMCP {
		page, err := jobspkg.ReadEnvironmentJobOutput(params)
		if err != nil {
			return uiEnvironmentJobOutput{}, err
		}
		return jobOutputToUI(page), nil
	}
	page, err := a.deps.readEnvironmentJobOutput(a.backgroundContext(), mcpEndpointForOpenResult(result), a.mcpBearer(tenant, environment), params)
	if err != nil {
		return uiEnvironmentJobOutput{}, a.wrapJobMCPErr(result, err)
	}
	return jobOutputToUI(page), nil
}

// CancelEnvironmentJob signals the job's work but leaves the supervisor alive to
// record the outcome.
func (a *App) CancelEnvironmentJob(input uiCancelJobInput) (uiEnvironmentJob, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	id := strings.TrimSpace(input.ID)
	if tenant == "" || environment == "" || id == "" {
		return uiEnvironmentJob{}, fmt.Errorf("tenant, environment, and job id are required")
	}
	params := jobspkg.CancelEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Signal:      strings.TrimSpace(input.Signal),
	}
	result, useMCP, err := a.jobsReachability(tenant, environment)
	if err != nil {
		return uiEnvironmentJob{}, err
	}
	if !useMCP {
		cancel, err := jobspkg.CancelEnvironmentJob(core.Context{}, params)
		if err != nil {
			return uiEnvironmentJob{}, err
		}
		return environmentJobToUI(cancel.Job), nil
	}
	cancel, err := a.deps.cancelEnvironmentJob(a.backgroundContext(), mcpEndpointForOpenResult(result), a.mcpBearer(tenant, environment), params)
	if err != nil {
		return uiEnvironmentJob{}, a.wrapJobMCPErr(result, err)
	}
	return environmentJobToUI(cancel.Job), nil
}

// jobsReachability picks the MCP edge, the local store, or an unreachable error.
func (a *App) jobsReachability(tenant, environment string) (config.OpenResult, bool, error) {
	result, err := open.ResolveOpen(a.deps.store, shared.OpenParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return config.OpenResult{}, false, err
	}
	mcpPort := config.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint(mcpPort) {
		return result, true, nil
	}
	if kind := a.classifyMCPUnreachable(mcpPort); kind == mcp.LocalMCPStaleForward {
		return config.OpenResult{}, false, wrapMCPUnreachableErrorWithKind(kind, errors.New(mcp.DescribeLocalMCPUnreachable(tenant, environment, mcpPort)))
	}
	return result, false, nil
}

// wrapJobMCPErr reports a dial failure as unreachable; other errors pass through.
func (a *App) wrapJobMCPErr(result config.OpenResult, err error) error {
	if err == nil || !isMCPDialFailure(err) {
		return err
	}
	mcpPort := config.MCPPortForResult(result)
	return wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
}

const defaultJobOutputPageBytes = 65536

func environmentJobsToUI(jobs []shared.EnvironmentJob) []uiEnvironmentJob {
	out := make([]uiEnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, environmentJobToUI(job))
	}
	return out
}

func jobOutputToUI(page jobspkg.EnvironmentJobOutput) uiEnvironmentJobOutput {
	return uiEnvironmentJobOutput{
		Job:        environmentJobToUI(page.Job),
		Offset:     page.Offset,
		NextOffset: page.NextOffset,
		Output:     page.Output,
		HasMore:    page.HasMore,
		Complete:   page.Complete,
	}
}

func environmentJobToUI(job shared.EnvironmentJob) uiEnvironmentJob {
	out := uiEnvironmentJob{
		ID:        job.ID,
		Name:      job.Name,
		State:     job.State,
		Kind:      job.Kind,
		AgentTool: job.AgentTool,
		Command:   job.Command,
		Dir:       job.Dir,
		ExitCode:  job.ExitCode,
	}
	if !job.StartedAt.IsZero() {
		out.StartedAtUnix = job.StartedAt.Unix()
	}
	if !job.EndedAt.IsZero() {
		out.EndedAtUnix = job.EndedAt.Unix()
	}
	if job.Progress != nil {
		out.Progress = &uiEnvironmentJobProgress{
			Activity:    job.Progress.Activity,
			LastMessage: job.Progress.LastMessage,
			Turns:       job.Progress.Turns,
			ToolsRun:    job.Progress.ToolsRun,
		}
	}
	return out
}
