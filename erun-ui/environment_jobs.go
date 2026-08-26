package main

import (
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

// LoadEnvironmentJobs lists an environment's retained jobs, newest first.
func (a *App) LoadEnvironmentJobs(tenant, environment string) ([]uiEnvironmentJob, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return nil, fmt.Errorf("tenant and environment are required")
	}
	jobs, err := eruncommon.LoadEnvironmentJobs(tenant, environment, time.Now())
	if err != nil {
		return nil, err
	}
	out := make([]uiEnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, environmentJobToUI(job))
	}
	return out, nil
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
	page, err := eruncommon.ReadEnvironmentJobOutput(eruncommon.ReadEnvironmentJobOutputParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Offset:      input.Offset,
		MaxBytes:    maxBytes,
	})
	if err != nil {
		return uiEnvironmentJobOutput{}, err
	}
	return uiEnvironmentJobOutput{
		Job:        environmentJobToUI(page.Job),
		Offset:     page.Offset,
		NextOffset: page.NextOffset,
		Output:     page.Output,
		HasMore:    page.HasMore,
		Complete:   page.Complete,
	}, nil
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
	result, err := eruncommon.CancelEnvironmentJob(eruncommon.Context{}, eruncommon.CancelEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Signal:      strings.TrimSpace(input.Signal),
	})
	if err != nil {
		return uiEnvironmentJob{}, err
	}
	return environmentJobToUI(result.Job), nil
}

// defaultJobOutputPageBytes is one screenful and then some, so the first read
// of a finished job is usually its whole output.
const defaultJobOutputPageBytes = 65536

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
