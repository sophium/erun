package agent

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/sophium/erun/erun-common/command/jobs"
	"github.com/sophium/erun/erun-mcp/tools/shared/jobresult"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
	"github.com/sophium/erun/erun-mcp/tools/shared/toolio"
)

// AgentInput is the agent-mode job tool's input.
type AgentInput struct {
	Agent           string            `json:"agent" jsonschema:"AI tool to run: claude or codex. erun invokes it in its streaming mode, so exec_job_output returns events while it works and exec_job_status reports its current activity rather than only running"`
	Prompt          string            `json:"prompt" jsonschema:"what the agent should do"`
	Tenant          string            `json:"tenant,omitempty" jsonschema:"tenant whose environment runs the job; defaults to the server tenant context, and must match it: this server only acts on its own environment"`
	Environment     string            `json:"environment,omitempty" jsonschema:"environment to run the job in; defaults to the server environment context, and must match it: this server only acts on its own environment"`
	Dir             string            `json:"dir,omitempty" jsonschema:"working directory to run from; defaults to the runtime repo root"`
	Env             map[string]string `json:"env,omitempty" jsonschema:"additional KEY=VALUE environment for the agent's own process, on top of what it inherits from the runtime pod -- e.g. raising CLAUDE_CODE_MAX_OUTPUT_TOKENS for one run. Values land in the job supervisor's argv, visible to anything that can list processes in this environment, so this is not where secrets belong. PATH, LD_PRELOAD, and a few other names that could redirect what the job executes are refused, as is any ERUN_ name"`
	Name            string            `json:"name,omitempty" jsonschema:"what the run is, shown wherever the environment reports as busy; defaults to agent"`
	ID              string            `json:"id,omitempty" jsonschema:"handle to address the job by; defaults to the name, so re-running the same named work keeps one stable handle"`
	MaxOutputBytes  int64             `json:"maxOutputBytes,omitempty" jsonschema:"cap on captured output in bytes; past it output is dropped and the job reports outputTruncated. Does not affect progress, which is folded from the tool's stream directly and keeps updating past the cap. Defaults to 16777216"`
	LeaseTTLSeconds int64             `json:"leaseTtlSeconds,omitempty" jsonschema:"activity lease TTL the job renews inside while it runs; defaults to 900"`
	Handoff         bool              `json:"handoff,omitempty" jsonschema:"mark this job as deliberately meant to outlive whatever starts it. When this call itself runs from inside another job's own work, that job otherwise waits for this one to reach a verdict before reporting its own outcome; set true for work meant to keep running past the caller's own turn on purpose (a release, a long render)"`
	Exclusive       bool              `json:"exclusive,omitempty" jsonschema:"declare that this run needs the environment to itself. While it holds the claim, every other job start in this environment is refused and told which job holds it -- plain jobs included, not only other exclusive ones. The claim expires without renewal and is reclaimed once its holder is gone, and work this job itself starts runs under the claim rather than being refused by it"`
	StartedByJobID  string            `json:"startedByJobId,omitempty" jsonschema:"internal: the job this call is being made on behalf of, so that job's own finish check can find this one as work it started. Only needed when this call itself runs from inside another job's own work AND reaches this tool through this environment's MCP edge rather than as a plain nested subprocess -- a nested subprocess (an agent's own Bash tool calling the erun CLI directly) already gets this for free from its own ERUN_JOB_ID and never needs to set it"`
	Preview         bool              `json:"preview,omitempty" jsonschema:"when true, resolve and trace the job without starting it"`
}

func AgentTool(runtime runtimecfg.RuntimeConfig) func(context.Context, *mcp.CallToolRequest, AgentInput) (*mcp.CallToolResult, jobresult.JobResult, error) {
	return func(_ context.Context, _ *mcp.CallToolRequest, input AgentInput) (*mcp.CallToolResult, jobresult.JobResult, error) {
		tenant, environment, err := runtimecfg.ResolveLocalTarget(runtime, input.Tenant, input.Environment)
		if err != nil {
			return nil, jobresult.JobResult{}, err
		}
		dir := strings.TrimSpace(input.Dir)
		if dir == "" {
			if dir, err = toolio.RuntimeRepoPath(runtime.Context); err != nil {
				return nil, jobresult.JobResult{}, err
			}
		}
		// emcp is not the erun binary, so the supervisor is the environment's erun.
		supervisor, err := jobs.ResolveErunExecutable()
		if err != nil {
			return nil, jobresult.JobResult{}, err
		}
		trace := new(bytes.Buffer)
		ctx := toolio.RuntimeCallContext(input.Preview, 0, nil, trace, trace)
		job, err := jobs.StartEnvironmentJob(ctx, jobs.StartEnvironmentJobParams{
			Tenant:         tenant,
			Environment:    environment,
			Name:           toolio.FirstNonEmpty(strings.TrimSpace(input.Name), "agent"),
			ID:             strings.TrimSpace(input.ID),
			Agent:          input.Agent,
			Prompt:         input.Prompt,
			Dir:            dir,
			Env:            input.Env,
			MaxOutputBytes: input.MaxOutputBytes,
			LeaseTTL:       time.Duration(input.LeaseTTLSeconds) * time.Second,
			Handoff:        input.Handoff,
			Exclusive:      input.Exclusive,
			StartedByJobID: input.StartedByJobID,
			SupervisorPath: supervisor,
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
