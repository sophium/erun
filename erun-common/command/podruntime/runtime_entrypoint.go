package podruntime

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/sampling"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
)

// The runtime entrypoint is PID 1 of the erun-devops image. Every preparation step is
// best-effort: a partly configured pod can be repaired from inside, a crash-looping one cannot.

// ErrRuntimeEntrypointUnsupported is returned off Linux, where init semantics do not apply.
var ErrRuntimeEntrypointUnsupported = errors.New("erun runtime-init runs only as the init of a Linux runtime container")

const (
	RuntimeBakedAgentsDir = "/etc/erun/agents"
)

// RuntimeEntrypointDevopsMode boots the environment and serves it until stopped.
const RuntimeEntrypointDevopsMode = "devops"

// RuntimeEntrypointParams is one entrypoint invocation.
type RuntimeEntrypointParams struct {
	// Command other than empty or "devops" runs as the supervised foreground process.
	Command  []string
	ERunPath string
	// Env nil means os.Getenv.
	Env     func(string) string
	HomeDir string
}

type runtimeBoot struct {
	env      func(string) string
	home     string
	erunPath string

	serviceAccountDir      string
	registryCredentialPath string
	platformAliasDir       string
	skillsRoot             string
	agentsRoot             string
	releaseDir             string
	procRoot               string
	sshdPath               string
}

func newRuntimeBoot(params RuntimeEntrypointParams) runtimeBoot {
	env := params.Env
	if env == nil {
		env = os.Getenv
	}
	return runtimeBoot{
		env:                    env,
		home:                   params.HomeDir,
		erunPath:               params.ERunPath,
		serviceAccountDir:      "/var/run/secrets/kubernetes.io/serviceaccount",
		registryCredentialPath: "/etc/erun/registry-credential/.dockerconfigjson",
		platformAliasDir:       "/etc/erun/platform-alias",
		skillsRoot:             core.RuntimeBakedSkillsDir,
		agentsRoot:             RuntimeBakedAgentsDir,
		releaseDir:             RuntimeReleaseDir,
		procRoot:               sampling.DefaultProcRoot,
		sshdPath:               "/usr/sbin/sshd",
	}
}

func (b runtimeBoot) get(key string) string {
	return strings.TrimSpace(b.env(key))
}

func (b runtimeBoot) getOr(key, fallback string) string {
	if value := b.get(key); value != "" {
		return value
	}
	return fallback
}

func (b runtimeBoot) flag(key string) bool {
	return runtimeEnvFlagEnabled(b.get(key))
}

func (b runtimeBoot) tenant() string      { return b.get("ERUN_TENANT") }
func (b runtimeBoot) environment() string { return b.get("ERUN_ENVIRONMENT") }

func (b runtimeBoot) hasTarget() bool {
	return b.tenant() != "" && b.environment() != ""
}

func (b runtimeBoot) repoDir() string {
	return b.getOr("ERUN_REPO_PATH", filepath.Join(b.home, "git", "erun"))
}

func (b runtimeBoot) outputsDir() string {
	return b.getOr("ERUN_OUTPUTS_DIR", filepath.Join(b.home, ".erun", "outputs"))
}

func (b runtimeBoot) configHome() string {
	return b.getOr("XDG_CONFIG_HOME", filepath.Join(b.home, ".config"))
}

func (b runtimeBoot) kubernetesContext() string {
	return b.getOr("ERUN_KUBERNETES_CONTEXT", providerconfig.InClusterKubernetesContext)
}

func (b runtimeBoot) path(rel ...string) string {
	return filepath.Join(append([]string{b.home}, rel...)...)
}

// runtimeEnvFlagEnabled accepts 1, true, yes and on, in any case.
func runtimeEnvFlagEnabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func runtimeEnvFlagDisabled(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return false
}

// describe returns "" when the step has nothing to do.
type runtimeBootStep struct {
	name     string
	describe func(runtimeBoot) string
	run      func(core.Context, runtimeBoot, *runtimeSupervisor) error
}

func runtimePreambleSteps() []runtimeBootStep {
	return []runtimeBootStep{
		{name: "kubeconfig", describe: describeRuntimeKubeconfig, run: writeRuntimeKubeconfig},
		{name: "ssh permissions", describe: describeRuntimeSSHPermissions, run: normalizeRuntimeSSHPermissions},
		{name: "outputs directory", describe: describeRuntimeOutputsDir, run: ensureRuntimeOutputsDir},
		{name: "registry credential", describe: describeRuntimeRegistryCredential, run: syncRuntimeRegistryCredential},
		{name: "git safe directory", describe: describeRuntimeGitSafeDirectory, run: ensureRuntimeGitSafeDirectory},
		{name: "sshd", describe: describeRuntimeSSHD, run: startRuntimeSSHD},
		{name: "environment monitor", describe: describeRuntimeEnvironmentMonitor, run: startRuntimeEnvironmentMonitor},
	}
}

// The MCP edge starts last so it never serves a half-prepared environment.
func runtimeDevopsSteps() []runtimeBootStep {
	return []runtimeBootStep{
		{name: "go build cache bound", describe: describeRuntimeCacheTrim, run: startRuntimeCacheTrim},
		{name: "runtime source", describe: describeRuntimeSource, run: ensureRuntimeSource},
		{name: "release link", describe: describeRuntimeReleaseLink, run: linkRuntimeRelease},
		{name: "erun config", describe: describeRuntimeBootConfig, run: writeRuntimeBootConfig},
		{name: "platform alias", describe: describeRuntimePlatformAlias, run: syncRuntimePlatformAlias},
		{name: "agent configuration", describe: describeRuntimeAgentConfig, run: configureRuntimeAgents},
		{name: "shell hook", describe: describeRuntimeShellHook, run: installRuntimeShellHook},
		{name: "MCP edge", describe: describeRuntimeMCP, run: startRuntimeMCP},
	}
}

func runRuntimeBootSteps(ctx core.Context, boot runtimeBoot, supervisor *runtimeSupervisor, steps []runtimeBootStep) {
	for _, step := range steps {
		if step.describe(boot) == "" {
			continue
		}
		if err := step.run(ctx, boot, supervisor); err != nil {
			ctx.Info(fmt.Sprintf("erun: runtime-init: %s: %v; continuing", step.name, err))
		}
	}
}

func traceRuntimeBootSteps(ctx core.Context, boot runtimeBoot, steps []runtimeBootStep) {
	for _, step := range steps {
		if description := step.describe(boot); description != "" {
			ctx.Trace("runtime-init: " + step.name + ": " + description)
		}
	}
}

func runtimeEntrypointCommand(args []string) []string {
	if len(args) == 0 || (len(args) == 1 && args[0] == RuntimeEntrypointDevopsMode) {
		return nil
	}
	return args
}

// RunRuntimeEntrypoint boots the runtime container and returns its exit code.
func RunRuntimeEntrypoint(ctx core.Context, params RuntimeEntrypointParams) (int, error) {
	boot := newRuntimeBoot(params)
	command := runtimeEntrypointCommand(params.Command)
	if ctx.DryRun {
		traceRuntimeEntrypointPlan(ctx, boot, command)
		return 0, nil
	}
	if hostOS := core.ResolveDetectedHostOS(); hostOS != model.HostOSLinux {
		return 1, fmt.Errorf("%w; this host is %s", ErrRuntimeEntrypointUnsupported, hostOS)
	}
	boot.env = runtimeEnvWithCloudMetadata(boot.env, func() runtimeCloudMetadata {
		return lookupRuntimeIMDS(runtimeIMDSEndpoint, runtimeIMDSTimeout)
	})
	supervisor := newRuntimeSupervisor(ctx)
	stop := supervisor.startInit()
	defer stop()

	runRuntimeBootSteps(ctx, boot, supervisor, runtimePreambleSteps())
	if command != nil {
		return supervisor.runForeground(command)
	}
	runRuntimeBootSteps(ctx, boot, supervisor, runtimeDevopsSteps())
	return supervisor.awaitTermination(), nil
}

func traceRuntimeEntrypointPlan(ctx core.Context, boot runtimeBoot, command []string) {
	traceRuntimeBootSteps(ctx, boot, runtimePreambleSteps())
	if command != nil {
		ctx.Trace("runtime-init: run " + core.FormatShellCommand("", command[0], command[1:]...) + " as the supervised foreground process and exit with its code")
		return
	}
	traceRuntimeBootSteps(ctx, boot, runtimeDevopsSteps())
	ctx.Trace("runtime-init: serve until the container is stopped, reaping orphans and forwarding termination signals")
}

// RuntimeGoBuildCacheTrimSettings reads the chart's cache bound. A zero or invalid cap means
// no bound, not a bound of zero.
func RuntimeGoBuildCacheTrimSettings(env func(string) string, homeDir string) (dir string, maxBytes int64, interval time.Duration) {
	get := func(key string) string { return strings.TrimSpace(env(key)) }
	dir = get("GOCACHE")
	if dir == "" {
		dir = filepath.Join(homeDir, ".cache", "go-build")
	}
	maxBytes = DefaultGoBuildCacheMaxBytes
	if raw := get("ERUN_GO_BUILD_CACHE_MAX_BYTES"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 0 {
			parsed = 0
		}
		maxBytes = parsed
	}
	interval = 30 * time.Minute
	if raw := get("ERUN_CACHE_TRIM_INTERVAL_SECONDS"); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds > 0 {
			interval = time.Duration(seconds) * time.Second
		}
	}
	return dir, maxBytes, interval
}

func (b runtimeBoot) runtimeCacheTrimSettings() (string, int64, time.Duration) {
	return RuntimeGoBuildCacheTrimSettings(b.env, b.home)
}

func describeRuntimeCacheTrim(boot runtimeBoot) string {
	dir, maxBytes, interval := boot.runtimeCacheTrimSettings()
	if maxBytes <= 0 {
		return ""
	}
	return fmt.Sprintf("bound the go build cache at %s to %d bytes now and every %s", dir, maxBytes, interval)
}

// The first pass runs in the background so the boot never waits on a large initial eviction.
func startRuntimeCacheTrim(ctx core.Context, boot runtimeBoot, supervisor *runtimeSupervisor) error {
	dir, maxBytes, interval := boot.runtimeCacheTrimSettings()
	supervisor.goLoop(func() bool {
		if _, err := TrimGoBuildCache(ctx, dir, maxBytes); err != nil {
			ctx.Info("erun: go build cache trim: " + err.Error())
		}
		return supervisor.sleep(interval)
	})
	return nil
}
