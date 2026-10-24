package jobs

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity"
)

// StartEnvironmentJobParams is the work to detach.
type StartEnvironmentJobParams struct {
	Tenant      string
	Environment string
	Name        string
	// ID defaults to the name, matching the lease store, so re-running the same
	// named work keeps one stable handle.
	ID      string
	Command []string
	// Agent names the AI tool to run as an agent job. Setting it replaces
	// Command: erun builds the tool's streaming invocation from Prompt, which is
	// the whole reason the kind exists — the tool's default mode reports nothing
	// until it exits.
	Agent  string
	Prompt string
	Dir    string
	// Env is additional environment for the job's own process, on top of what it
	// inherits from the environment's runtime pod (e.g. raising
	// CLAUDE_CODE_MAX_OUTPUT_TOKENS for one agent run). Values land in the
	// supervisor's argv, which anything able to list processes in this
	// environment can read — this is not where secrets belong. See
	// normalizeEnvironmentJobEnv for the names it refuses to set.
	Env map[string]string
	// MaxOutputBytes caps the captured output; zero takes the default.
	MaxOutputBytes int64
	// LeaseTTL is how long the job's activity lease holds between renewals; the
	// supervisor renews well inside it for as long as the work runs.
	LeaseTTL time.Duration
	// Handoff marks this job as deliberately meant to outlive whatever starts
	// it, excluding it from that caller's own finish check entirely (see
	// EnvironmentJob.Handoff). Only meaningful when this start itself runs
	// from inside another job's own work.
	Handoff bool
	// Exclusive declares that this job needs the environment to itself: it
	// claims EnvironmentActivityLeaseScopeEnvironment for its lifetime, and
	// while it holds that claim every other job start here is refused (see
	// job_exclusive.go). Resolution rewrites this field to what the start
	// actually did, so a job running under an ancestor's existing claim reads
	// as not holding one of its own.
	Exclusive bool
	// StartedByJobID is an explicit override for the job this new work should
	// record as its own parent (see EnvironmentJob.StartedByJobID). Leave it
	// empty for the common case: the supervisor this spawns inherits its own
	// process's ERUN_JOB_ID for free when the start itself runs as a plain
	// nested subprocess of another job's work (an agent's own Bash tool). Set it explicitly only when forwarding this start through a
	// channel that cannot carry that inheritance itself — the MCP edge, most
	// notably, since a request reaching it crosses into that server's own
	// long-lived process, which has no ERUN_JOB_ID of its own regardless of
	// how deep the logical nesting is on the calling side. See
	// CurrentEnvironmentJobID.
	StartedByJobID string
	// SupervisorPath is the erun executable that will supervise the job. Each
	// transport resolves it — the CLI is already that executable, the MCP server
	// finds it on the environment's PATH — so this package stays free of any
	// assumption about which binary is calling.
	SupervisorPath string
}

func (p StartEnvironmentJobParams) normalize() (StartEnvironmentJobParams, error) {
	p.Tenant = strings.TrimSpace(p.Tenant)
	p.Environment = strings.TrimSpace(p.Environment)
	p.Name = strings.TrimSpace(p.Name)
	p.StartedByJobID = strings.TrimSpace(p.StartedByJobID)
	id, err := normalizeEnvironmentJobIdentity(p.Tenant, p.Environment, p.Name, p.ID)
	if err != nil {
		return p, err
	}
	p.ID = id
	if p.Agent, p.Command, err = resolveEnvironmentJobWork(p.Agent, p.Prompt, p.Command); err != nil {
		return p, err
	}
	if p.Env, err = normalizeEnvironmentJobEnv(p.Env); err != nil {
		return p, err
	}
	if strings.TrimSpace(p.SupervisorPath) == "" {
		return p, fmt.Errorf("job supervisor executable is required")
	}
	if p.MaxOutputBytes, err = normalizeEnvironmentJobOutputLimit(p.MaxOutputBytes); err != nil {
		return p, err
	}
	if p.LeaseTTL, err = normalizeEnvironmentJobLeaseTTL(p.LeaseTTL); err != nil {
		return p, err
	}
	if p.Dir, err = resolveEnvironmentJobDir(p.Dir); err != nil {
		return p, err
	}
	return p, nil
}

// resolveEnvironmentJobDir resolves the directory the work runs in, and proves
// it usable before anything is detached.
//
// The supervisor is started with no inherited working directory, so a relative
// dir cannot mean "relative to the supervisor" — left alone it resolves against
// whatever directory the transport happens to be sitting in, which is not the
// repository. Anchor it to the project root instead, the same reading every
// other repo-facing surface gives a path, so a caller can name a subdirectory
// here exactly as they would to `raw`.
//
// The check happens now rather than at exec because Go reports an unusable
// cmd.Dir as an ENOENT naming the *binary*: an unverified dir surfaces as a
// missing shell, sending the reader after a broken image instead of a bad path.
func resolveEnvironmentJobDir(dir string) (string, error) {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "", nil
	}
	resolved := dir
	if !filepath.IsAbs(resolved) {
		if _, root, err := config.FindProjectRoot(); err == nil {
			resolved = filepath.Join(root, resolved)
		} else if abs, absErr := filepath.Abs(resolved); absErr == nil {
			resolved = abs
		}
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("job dir %q does not exist (resolved to %s)", dir, resolved)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("job dir %q is not a directory (resolved to %s)", dir, resolved)
	}
	return resolved, nil
}

// normalizeEnvironmentJobIdentity resolves the target and the handle a job is
// addressed by, shared by starting and attaching. The id defaults to the name,
// matching the lease store, so re-running the same named work keeps one handle.
func normalizeEnvironmentJobIdentity(tenant, environment, name, id string) (string, error) {
	if err := model.ErrMissingTenantOrEnvironment("resolve environment job identity", tenant, environment); err != nil {
		return "", err
	}
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("job name is required")
	}
	value := strings.TrimSpace(id)
	if value == "" {
		value = name
	}
	return resolveEnvironmentJobID(value)
}

// resolveEnvironmentJobWork settles what the job runs. An agent job names a tool
// and a prompt and erun builds the argv; a command job brings its own. Naming
// both is refused rather than resolved, because a caller that meant one of them
// would otherwise silently get the other.
func resolveEnvironmentJobWork(agent, prompt string, command []string) (string, []string, error) {
	if strings.TrimSpace(agent) == "" {
		if strings.TrimSpace(prompt) != "" {
			return "", nil, fmt.Errorf("a prompt only applies to an agent job; name the agent tool or pass a command")
		}
		if len(command) == 0 || strings.TrimSpace(command[0]) == "" {
			return "", nil, fmt.Errorf("job command is required")
		}
		return "", command, nil
	}
	if len(command) > 0 {
		return "", nil, fmt.Errorf("an agent job runs the tool's own streaming invocation; pass a prompt, not a command")
	}
	tool, err := NormalizeAgentJobTool(agent)
	if err != nil {
		return "", nil, err
	}
	built, err := AgentJobCommand(tool, prompt)
	if err != nil {
		return "", nil, err
	}
	return tool, built, nil
}

// jobEnvKeyPattern is the shape a caller-supplied job env var name must take:
// a plain identifier, the only thing that is ever a real environment variable
// name on the platforms erun runs on.
var jobEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jobEnvDeniedKeys are names a caller-supplied job env must never override.
// PATH/LD_PRELOAD/LD_LIBRARY_PATH/DYLD_* can redirect what the job's own
// process executes or dynamically loads — a code-execution hijack, not a
// tuning knob — and HOME/SHELL/IFS can subvert whatever script the job's
// command runs.
var jobEnvDeniedKeys = map[string]struct{}{
	"PATH": {}, "LD_PRELOAD": {}, "LD_LIBRARY_PATH": {},
	"DYLD_INSERT_LIBRARIES": {}, "DYLD_LIBRARY_PATH": {},
	"HOME": {}, "SHELL": {}, "IFS": {},
}

// normalizeEnvironmentJobEnv validates a caller-supplied job environment
// override. An `ERUN_` name is refused outright: that prefix is erun's own
// seam for redirecting its subprocess lookups (`ERUN_<NAME>_BIN`) and its
// detection overrides, and a job that later shells out to erun itself must not
// be able to redirect those from the outside.
func normalizeEnvironmentJobEnv(env map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(env))
	for key, value := range env {
		key = strings.TrimSpace(key)
		if !jobEnvKeyPattern.MatchString(key) {
			return nil, fmt.Errorf("job env var name %q is not a valid environment variable name", key)
		}
		upper := strings.ToUpper(key)
		if _, denied := jobEnvDeniedKeys[upper]; denied || strings.HasPrefix(upper, "ERUN_") {
			return nil, fmt.Errorf("job env var %q may not be set: it can redirect what the job's process executes or loads", key)
		}
		out[key] = value
	}
	return out, nil
}

// sortedEnvironmentJobEnvPairs renders a job env override as "KEY=VALUE"
// pairs in a stable order, so the supervisor argv and its dry-run trace never
// reorder between runs of the same request.
func sortedEnvironmentJobEnvPairs(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+env[key])
	}
	return pairs
}

// ensureAgentJobToolAvailable refuses an agent job before anything is spent —
// no activity lease, no log file, no supervisor process — when the named tool
// is not installed in this environment. That precondition is knowable up
// front; starting anyway trades a direct, actionable refusal for the tool's
// own failure a minute later, which for an unconfigured environment reads as
// a credential problem with no credential fix.
func ensureAgentJobToolAvailable(ctx core.Context, tool string) error {
	if strings.TrimSpace(tool) == "" {
		return nil
	}
	ctx.Trace(fmt.Sprintf("job: resolving agent tool %q in this environment", tool))
	// cmd.Err carries Go's own exec.LookPath phrasing, which is an
	// implementation detail rather than something to hand to the caller
	// verbatim; the tool name and "not available" already say what happened.
	if cmd := core.Command(tool); cmd.Err != nil {
		err := fmt.Errorf("agent tool %q is not available in this environment; install and configure it here, or start the job with a different --agent", tool)
		ctx.Trace(fmt.Sprintf("job: resolving agent tool %q failed: %v", tool, err))
		return err
	}
	return nil
}

// environmentJobKind names the kind a recorded tool implies, so the record and
// every reader agree without either re-deriving it.
func environmentJobKind(agentTool string) string {
	if strings.TrimSpace(agentTool) == "" {
		return EnvironmentJobKindCommand
	}
	return EnvironmentJobKindAgent
}

func normalizeEnvironmentJobOutputLimit(limit int64) (int64, error) {
	if limit == 0 {
		return DefaultEnvironmentJobOutputLimitBytes, nil
	}
	if limit < 0 {
		return 0, fmt.Errorf("max output bytes must not be negative")
	}
	return limit, nil
}

func normalizeEnvironmentJobLeaseTTL(ttl time.Duration) (time.Duration, error) {
	if ttl == 0 {
		return activity.DefaultEnvironmentActivityLeaseTTL, nil
	}
	if ttl < 0 {
		return 0, fmt.Errorf("lease ttl must be greater than zero")
	}
	return ttl, nil
}
