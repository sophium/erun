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
	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// The runtime entrypoint is what the erun-devops image runs as PID 1. It prepares
// the container from the environment the runtime chart injects -- kubeconfig,
// credentials, erun config, agent configuration -- starts the long-lived services
// the pod needs (sshd and its activity proxy, the activity monitor, the go build
// cache bound, the MCP edge), and then does what an init does: reaps orphans,
// forwards termination signals to what it started, and exits with the code of the
// work it was running.
//
// Every preparation step is best-effort. A step that fails is reported on the
// container log and the boot continues, because a pod that comes up partly
// configured can still be repaired from inside (`erun doctor`), while one that
// crash-loops cannot be reached at all.

// ErrRuntimeEntrypointUnsupported is returned off Linux: the entrypoint is an init
// for the runtime container, and reaping and signal handling are Linux process
// semantics.
var ErrRuntimeEntrypointUnsupported = errors.New("erun runtime-init runs only as the init of a Linux runtime container")

const (
	RuntimeBakedAgentsDir = "/etc/erun/agents"
)

// RuntimeEntrypointDevopsMode is the image's default command: boot the
// environment and keep serving it until the container is stopped.
const RuntimeEntrypointDevopsMode = "devops"

// RuntimeEntrypointParams is one entrypoint invocation.
type RuntimeEntrypointParams struct {
	// Command is the container's command. Empty or "devops" boots the
	// environment; anything else runs after the common preparation as the
	// supervised foreground process whose exit code the container exits with.
	Command []string
	// ERunPath is the erun executable the long-lived services invoke.
	ERunPath string
	// Env reads the container environment; nil means os.Getenv.
	Env func(string) string
	// HomeDir is the erun user's home.
	HomeDir string
}

// runtimeBoot is the container as the entrypoint sees it: the injected
// environment, the home directory, and the fixed paths the image and chart
// share. Tests point the paths at fixtures.
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
		procRoot:               activity.DefaultProcRoot,
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
	return b.getOr("ERUN_KUBERNETES_CONTEXT", cloud.InClusterKubernetesContext)
}

func (b runtimeBoot) path(rel ...string) string {
	return filepath.Join(append([]string{b.home}, rel...)...)
}

// runtimeEnvFlagEnabled reads the chart's boolean variables the way the shell
// entrypoint always did: 1, true, yes and on, in any case.
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

// runtimeBootStep is one preparation step. describe says what the step would do
// for this container, or "" when it has nothing to do; run does it.
type runtimeBootStep struct {
	name     string
	describe func(runtimeBoot) string
	run      func(core.Context, runtimeBoot, *runtimeSupervisor) error
}

// runtimePreambleSteps run in every mode, before the container's command.
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

// runtimeDevopsSteps boot the environment. The MCP edge starts last, so it never
// serves a half-prepared environment.
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

// runtimeEntrypointCommand returns the command to supervise, or nil for the
// devops boot.
func runtimeEntrypointCommand(args []string) []string {
	if len(args) == 0 || (len(args) == 1 && args[0] == RuntimeEntrypointDevopsMode) {
		return nil
	}
	return args
}

// RunRuntimeEntrypoint boots the runtime container and returns the exit code the
// container should end with.
func RunRuntimeEntrypoint(ctx core.Context, params RuntimeEntrypointParams) (int, error) {
	boot := newRuntimeBoot(params)
	command := runtimeEntrypointCommand(params.Command)
	if ctx.DryRun {
		traceRuntimeEntrypointPlan(ctx, boot, command)
		return 0, nil
	}
	if hostOS := core.ResolveDetectedHostOS(); hostOS != core.HostOSLinux {
		return 1, fmt.Errorf("%w; this host is %s", ErrRuntimeEntrypointUnsupported, hostOS)
	}
	// Only the real boot asks the instance metadata service; a dry run traces
	// the plan from what the chart injected.
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

// RuntimeGoBuildCacheTrimSettings reads the go build cache bound the runtime
// chart renders: the cache directory, its cap, and how often the boot re-applies
// it. A cap of 0, or one that is not a number, means no bound rather than a
// bound of zero, which would clear the cache and make every build cold.
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

// startRuntimeCacheTrim bounds the go build cache for as long as the container
// lives. The first pass runs in the background because it is the migration: an
// environment already holding tens of gigabytes is brought under the cap by it,
// and the boot must not wait on that. It repeats because a container outlives
// the boot that started it.
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
