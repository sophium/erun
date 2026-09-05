package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The desktop writes into the job store — Investigate starts a job there — and
// until now could not display the job it had just created. These bindings give
// the Jobs surface the same three reads the CLI and MCP already have, plus the
// one write that matters while a job is running.
//
// Output is paged rather than streamed: a page is what the operator's scroll
// position can absorb, and an offset the caller passes back is what makes a
// second read continue rather than repeat.
//
// A remote-agent/runtime environment's jobs run in its pod, not on the host,
// so these reads go through the env's own MCP edge whenever it is reachable —
// the same branch LoadIdleStatus already uses to tell a pod-backed answer from
// a locally-guessed one. The local store is still consulted when the edge is
// not reachable: it is where the desktop's own Investigate job is recorded
// (that job runs on the host regardless of the environment's type), and for an
// environment nobody has opened yet it is the only truthful answer there is —
// no pod is running to have missed any jobs. A stale forward (the port is
// held but the edge never answers) is different: a pod may well be running
// jobs behind it, so that case is reported as unreachable rather than
// silently read as empty.

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
		jobs, err := eruncommon.LoadEnvironmentJobs(tenant, environment, time.Now())
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
	params := eruncommon.ReadEnvironmentJobOutputParams{
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
		page, err := eruncommon.ReadEnvironmentJobOutput(params)
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

// CancelEnvironmentJob signals a running job's work. The supervisor is left
// alone so it survives to record the outcome, which is why a cancelled job
// still reads back as a normal exited job rather than vanishing.
func (a *App) CancelEnvironmentJob(input uiCancelJobInput) (uiEnvironmentJob, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	id := strings.TrimSpace(input.ID)
	if tenant == "" || environment == "" || id == "" {
		return uiEnvironmentJob{}, fmt.Errorf("tenant, environment, and job id are required")
	}
	params := eruncommon.CancelEnvironmentJobParams{
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
		cancel, err := eruncommon.CancelEnvironmentJob(eruncommon.Context{}, params)
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

// jobsReachability decides how a job read or write should reach its answer:
// through the environment's own MCP edge when it is reachable, or through the
// local store when the environment is legitimately not running -- no pod is
// there to have missed anything, and the local store is also where the
// desktop's own Investigate job is recorded regardless of the environment's
// type. A stale forward (the port is held but the edge never answers) is
// neither: a pod may well be running jobs behind it that the local store
// cannot see, so that case is reported as unreachable rather than read as
// empty.
func (a *App) jobsReachability(tenant, environment string) (eruncommon.OpenResult, bool, error) {
	result, err := eruncommon.ResolveOpen(a.deps.store, eruncommon.OpenParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return eruncommon.OpenResult{}, false, err
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	if a.deps.canReachMCPEndpoint(mcpPort) {
		return result, true, nil
	}
	if kind := a.classifyMCPUnreachable(mcpPort); kind == eruncommon.LocalMCPStaleForward {
		return eruncommon.OpenResult{}, false, wrapMCPUnreachableErrorWithKind(kind, errors.New(eruncommon.DescribeLocalMCPUnreachable(tenant, environment, mcpPort)))
	}
	return result, false, nil
}

// wrapJobMCPErr classifies a failure from a pod-backed job call: a dial
// failure means the edge dropped mid-call, reported the same way an
// upfront-unreachable read is; any other failure (including "this runtime
// image predates the job_* tools") passes through unchanged.
func (a *App) wrapJobMCPErr(result eruncommon.OpenResult, err error) error {
	if err == nil || !isMCPDialFailure(err) {
		return err
	}
	mcpPort := eruncommon.MCPPortForResult(result)
	return wrapMCPUnreachableErrorWithKind(a.classifyMCPUnreachable(mcpPort), err)
}

// defaultJobOutputPageBytes is one screenful and then some, so the first read
// of a finished job is usually its whole output.
const defaultJobOutputPageBytes = 65536

func environmentJobsToUI(jobs []eruncommon.EnvironmentJob) []uiEnvironmentJob {
	out := make([]uiEnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, environmentJobToUI(job))
	}
	return out
}

func jobOutputToUI(page eruncommon.EnvironmentJobOutput) uiEnvironmentJobOutput {
	return uiEnvironmentJobOutput{
		Job:        environmentJobToUI(page.Job),
		Offset:     page.Offset,
		NextOffset: page.NextOffset,
		Output:     page.Output,
		HasMore:    page.HasMore,
		Complete:   page.Complete,
	}
}

func environmentJobToUI(job eruncommon.EnvironmentJob) uiEnvironmentJob {
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
