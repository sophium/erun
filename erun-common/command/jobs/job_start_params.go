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
	"github.com/sophium/erun/erun-common/infra/activity/activitylease"
)

// StartEnvironmentJobParams is the work to detach.
type StartEnvironmentJobParams struct {
	Tenant      string
	Environment string
	Name        string
	// ID defaults to the name, so re-running named work keeps one handle.
	ID      string
	Command []string
	// Agent names an AI tool; erun builds its streaming invocation from Prompt
	// in place of Command.
	Agent  string
	Prompt string
	Dir    string
	// Env is extra environment for the job's process. It lands in the supervisor's
	// argv, readable by anyone listing processes, so never put secrets here.
	Env            map[string]string
	MaxOutputBytes int64
	LeaseTTL       time.Duration
	// Handoff excludes this job from its starting job's finish check.
	Handoff bool
	// Exclusive claims the whole environment; resolution rewrites it to whether
	// this job actually holds its own claim.
	Exclusive bool
	// StartedByJobID overrides the inherited ERUN_JOB_ID parent link, for starts
	// forwarded through the MCP edge, whose process has none.
	StartedByJobID string
	// SupervisorPath is the erun executable that supervises the job; each transport resolves it.
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

// resolveEnvironmentJobDir anchors a relative dir to the project root (the
// supervisor has no working directory) and checks it now: Go reports a bad
// cmd.Dir as ENOENT naming the binary.
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

// normalizeEnvironmentJobIdentity validates the target and defaults the id to the name.
func normalizeEnvironmentJobIdentity(tenant, environment, name, id string) (string, error) {
	if err := core.ErrMissingTenantOrEnvironment("resolve environment job identity", tenant, environment); err != nil {
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

// resolveEnvironmentJobWork settles what the job runs; naming both an agent and
// a command is refused rather than silently picking one.
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

var jobEnvKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// jobEnvDeniedKeys can hijack what the job executes or loads, or subvert its scripts.
var jobEnvDeniedKeys = map[string]struct{}{
	"PATH": {}, "LD_PRELOAD": {}, "LD_LIBRARY_PATH": {},
	"DYLD_INSERT_LIBRARIES": {}, "DYLD_LIBRARY_PATH": {},
	"HOME": {}, "SHELL": {}, "IFS": {},
}

// normalizeEnvironmentJobEnv also refuses ERUN_ names, which redirect erun's own
// subprocess lookups and detection.
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

// sortedEnvironmentJobEnvPairs renders KEY=VALUE pairs in a stable order.
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

// ensureAgentJobToolAvailable refuses before anything is spent when the tool is
// missing, rather than failing later like a credential problem.
func ensureAgentJobToolAvailable(ctx core.Context, tool string) error {
	if strings.TrimSpace(tool) == "" {
		return nil
	}
	ctx.Trace(fmt.Sprintf("job: resolving agent tool %q in this environment", tool))
	if cmd := core.Command(tool); cmd.Err != nil {
		err := fmt.Errorf("agent tool %q is not available in this environment; install and configure it here, or start the job with a different --agent", tool)
		ctx.Trace(fmt.Sprintf("job: resolving agent tool %q failed: %v", tool, err))
		return err
	}
	return nil
}

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
		return activitylease.DefaultEnvironmentActivityLeaseTTL, nil
	}
	if ttl < 0 {
		return 0, fmt.Errorf("lease ttl must be greater than zero")
	}
	return ttl, nil
}
