package envops

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	jobspkg "github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-common/infra/mcp"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A remote-agent/runtime env's jobs run in its pod, so reads go through its MCP
// edge when reachable. The local store answers when the env is not open (it also
// holds the desktop's own Investigate job); a stale forward is reported as
// unreachable, never as empty.

// LoadEnvironmentJobs lists an environment's retained jobs, newest first.
func (s *Service) LoadEnvironmentJobs(tenant, environment string) ([]EnvironmentJob, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := uimodel.ErrMissingTenantOrEnvironment("load environment jobs", tenant, environment); err != nil {
		return nil, err
	}
	result, useMCP, err := s.jobsReachability(tenant, environment)
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
	jobs, err := s.d.LoadJobs(s.d.Context(), s.d.MCPEndpoint(result), s.d.MCPBearer(tenant, environment))
	if err != nil {
		return nil, s.wrapJobMCPErr(result, err)
	}
	return environmentJobsToUI(jobs), nil
}

// ReadEnvironmentJobOutput returns one page of a job's captured output.
func (s *Service) ReadEnvironmentJobOutput(input JobOutputInput) (EnvironmentJobOutput, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	id := strings.TrimSpace(input.ID)
	if tenant == "" || environment == "" || id == "" {
		return EnvironmentJobOutput{}, fmt.Errorf("tenant, environment, and job id are required")
	}
	maxBytes := input.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultJobOutputPageBytes
	}
	params := model.ReadEnvironmentJobOutputParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Offset:      input.Offset,
		MaxBytes:    maxBytes,
	}
	result, useMCP, err := s.jobsReachability(tenant, environment)
	if err != nil {
		return EnvironmentJobOutput{}, err
	}
	if !useMCP {
		page, err := jobspkg.ReadEnvironmentJobOutput(params)
		if err != nil {
			return EnvironmentJobOutput{}, err
		}
		return jobOutputToUI(page), nil
	}
	page, err := s.d.ReadJobOutput(s.d.Context(), s.d.MCPEndpoint(result), s.d.MCPBearer(tenant, environment), params)
	if err != nil {
		return EnvironmentJobOutput{}, s.wrapJobMCPErr(result, err)
	}
	return jobOutputToUI(page), nil
}

// CancelEnvironmentJob signals the job's work but leaves the supervisor alive to
// record the outcome.
func (s *Service) CancelEnvironmentJob(input CancelJobInput) (EnvironmentJob, error) {
	tenant := strings.TrimSpace(input.Tenant)
	environment := strings.TrimSpace(input.Environment)
	id := strings.TrimSpace(input.ID)
	if tenant == "" || environment == "" || id == "" {
		return EnvironmentJob{}, fmt.Errorf("tenant, environment, and job id are required")
	}
	params := model.CancelEnvironmentJobParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          id,
		Signal:      strings.TrimSpace(input.Signal),
	}
	result, useMCP, err := s.jobsReachability(tenant, environment)
	if err != nil {
		return EnvironmentJob{}, err
	}
	if !useMCP {
		cancel, err := jobspkg.CancelEnvironmentJob(core.Context{}, params)
		if err != nil {
			return EnvironmentJob{}, err
		}
		return environmentJobToUI(cancel.Job), nil
	}
	cancel, err := s.d.CancelJob(s.d.Context(), s.d.MCPEndpoint(result), s.d.MCPBearer(tenant, environment), params)
	if err != nil {
		return EnvironmentJob{}, s.wrapJobMCPErr(result, err)
	}
	return environmentJobToUI(cancel.Job), nil
}

// jobsReachability picks the MCP edge, the local store, or an unreachable error.
func (s *Service) jobsReachability(tenant, environment string) (config.OpenResult, bool, error) {
	result, err := resolve.ResolveOpen(s.d.Store, model.OpenParams{
		Tenant:      tenant,
		Environment: environment,
	})
	if err != nil {
		return config.OpenResult{}, false, err
	}
	mcpPort := config.MCPPortForResult(result)
	if s.d.CanReachMCPEndpoint(mcpPort) {
		return result, true, nil
	}
	if kind := s.classifyMCPUnreachable(mcpPort); kind == mcp.LocalMCPStaleForward {
		return config.OpenResult{}, false, s.d.WrapMCPUnreachable(kind, errors.New(mcp.DescribeLocalMCPUnreachable(tenant, environment, mcpPort)))
	}
	return result, false, nil
}

// wrapJobMCPErr reports a dial failure as unreachable; other errors pass through.
func (s *Service) wrapJobMCPErr(result config.OpenResult, err error) error {
	if err == nil || !s.d.IsMCPDialFailure(err) {
		return err
	}
	mcpPort := config.MCPPortForResult(result)
	return s.d.WrapMCPUnreachable(s.classifyMCPUnreachable(mcpPort), err)
}

const defaultJobOutputPageBytes = 65536

func environmentJobsToUI(jobs []jobstate.EnvironmentJob) []EnvironmentJob {
	out := make([]EnvironmentJob, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, environmentJobToUI(job))
	}
	return out
}

func jobOutputToUI(page jobspkg.EnvironmentJobOutput) EnvironmentJobOutput {
	return EnvironmentJobOutput{
		Job:        environmentJobToUI(page.Job),
		Offset:     page.Offset,
		NextOffset: page.NextOffset,
		Output:     page.Output,
		HasMore:    page.HasMore,
		Complete:   page.Complete,
	}
}

func environmentJobToUI(job jobstate.EnvironmentJob) EnvironmentJob {
	out := EnvironmentJob{
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
		out.Progress = &EnvironmentJobProgress{
			Activity:    job.Progress.Activity,
			LastMessage: job.Progress.LastMessage,
			Turns:       job.Progress.Turns,
			ToolsRun:    job.Progress.ToolsRun,
		}
	}
	return out
}
