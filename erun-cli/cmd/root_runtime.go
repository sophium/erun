package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/cmd/build"
	"github.com/sophium/erun/cmd/initcmd"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/cloudcli"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/primitive/build/linuxpkg"
	"github.com/sophium/erun/erun-common/primitive/build/script"
	buildspec "github.com/sophium/erun/erun-common/primitive/build/spec"
	deployspec "github.com/sophium/erun/erun-common/primitive/deploy/spec"
	"github.com/sophium/erun/erun-common/primitive/open/inittarget"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

type rootStore interface {
	seam.BootstrapStore
	config.OpenStore
	seam.DockerStore
	seam.DeployStore
}

func newRootCommand(runRoot func(*cobra.Command, []string) error) *cobra.Command {
	var verbosity int

	cmd := &cobra.Command{
		Use:              "erun",
		Short:            "Environment Runner",
		Long:             "erun helps to run and manage multiple tenants/environments.\n\nVerbosity levels:\n  (none) info: only erun status output; external tool output is suppressed\n  -v     debug: stream external tool output with high verbosity (helm --debug, kubectl --v=4, docker --progress=plain)\n  -vv    trace: debug output plus erun command-trace lines for every action and decision\n\nDry-run:\n  --dry-run runs the same resolution flow but skips mutating operations and implies trace verbosity.\n\nTiming:\n  --time prints the elapsed runtime after the command finishes",
		Example:          "  erun deploy --dry-run\n  erun -v deploy\n  erun -vv init -y\n  alias tenant-environment='eval \"$(erun open tenant environment --no-shell)\"'",
		Args:             cobra.MaximumNArgs(2),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		PersistentPreRun: AuditCommand,
		RunE:             runRoot,
	}
	clicmd.AddDryRunFlag(cmd)
	AddTimeFlag(cmd)
	clicmd.AddOutputFlag(cmd)
	clicmd.WrapCommandTreeWithElapsedTime(cmd)
	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", VerboseFlagUsage)
	return cmd
}

// tenantSelectionUnavailableExitCode marks a run that needed a tenant choice but had no
// terminal to ask on; distinct from failure and from the 124-127 codes.
const tenantSelectionUnavailableExitCode = 128

// bootstrapInitExitError preserves a bootstrap-init failure's exit code. An unresolvable
// platform alias keeps its own sentinel (127).
func bootstrapInitExitError(err error) error {
	if errors.Is(err, config.ErrNotInGitRepository) {
		return internal.MarkReported(config.ErrNotInGitRepository)
	}
	var unavailable model.TenantSelectionUnavailableError
	if errors.As(err, &unavailable) {
		return internal.WithExitCode(err, tenantSelectionUnavailableExitCode)
	}
	return err
}

func newRunInit(store seam.BootstrapStore, findProjectRoot seam.ProjectFinderFunc, promptRunner prompt.PromptRunner, selectRunner prompt.SelectRunner, listKubernetesContexts prompt.KubernetesContextsLister, ensureKubernetesNamespace seam.NamespaceEnsurerFunc, waitForRemoteRuntime seam.RemoteRuntimeWaitFunc, runRemoteCommand seam.RemoteCommandRunnerFunc, deployHelmChart seam.HelmChartDeployerFunc) func(core.Context, seam.BootstrapInitParams) error {
	return func(ctx core.Context, params seam.BootstrapInitParams) error {
		ctx = cloudcli.WithCloudContextPreflight(ctx, store)
		params.RuntimeVersionDefault = shared.CurrentBuildInfo().Version
		_, err := setup.RunBootstrapInitWithDependencies(setup.BootstrapInitDependencies{
			Store:           setup.TraceBootstrapStore(ctx, store),
			FindProjectRoot: findProjectRoot,
			SelectTenant: func(tenants []model.TenantConfig) (model.TenantSelectionResult, error) {
				return initcmd.SelectTenantPrompt(selectRunner, tenants)
			},
			TenantSelectionUnavailable: initcmd.TenantSelectionUnavailable(),
			Confirm: func(label string) (bool, error) {
				return prompt.ConfirmPrompt(promptRunner, label)
			},
			PromptKubernetesContext: func(label string) (string, error) {
				return initcmd.KubernetesContextPrompt(promptRunner, selectRunner, listKubernetesContexts, label)
			},
			PromptContainerRegistry: func(label string) (string, error) {
				return initcmd.ContainerRegistryPrompt(promptRunner, label)
			},
			PromptRemoteRepositoryURL: func(label string) (string, error) {
				return initcmd.RemoteRepositoryURLPrompt(promptRunner, label)
			},
			PromptCodeCommitSSHKeyID: func(label string) (string, error) {
				return initcmd.CodeCommitSSHKeyIDPrompt(promptRunner, label)
			},
			EnsureKubernetesNamespace: setup.TraceNamespaceEnsurer(ctx, ensureKubernetesNamespace),
			LoadProjectConfig:         config.LoadProjectConfig,
			SaveProjectConfig:         setup.TraceProjectConfigSaver(ctx, config.SaveProjectConfig),
			WaitForRemoteRuntime:      waitForRemoteRuntime,
			RunRemoteCommand:          runRemoteCommand,
			DeployHelmChart:           deployHelmChart,
			Context:                   ctx,
		}, params)
		if err != nil {
			return bootstrapInitExitError(err)
		}
		ctx.TraceCommand("", "ensure-agent-instructions")
		ctx.TraceCommand("", "ensure-claude-settings")
		if ctx.DryRun {
			return nil
		}
		if homeDir, ensureErr := os.UserHomeDir(); ensureErr != nil {
			ctx.Logger.Debug("could not ensure global agent instructions: " + ensureErr.Error())
		} else if ensureErr := podruntime.EnsureGlobalAgentInstructions(homeDir); ensureErr != nil {
			ctx.Logger.Debug("could not ensure global agent instructions: " + ensureErr.Error())
		}
		if ensureErr := internal.EnsureClaudeSettings(); ensureErr != nil {
			ctx.Logger.Debug("could not ensure claude settings: " + ensureErr.Error())
		}
		return nil
	}
}

func resolveRuntimeDeploySpecForOpen(ctx core.Context, store seam.DeployStore, findProjectRoot seam.ProjectFinderFunc, resolveDockerBuildContext seam.BuildContextResolverFunc, resolveKubernetesDeployContext seam.DeployContextResolverFunc, now seam.NowFunc, buildInfo model.BuildInfo, target config.OpenResult, allowLocalBuilds bool) (seam.DeploySpec, error) {
	spec, err := deployspec.ResolveOpenRuntimeDeploySpec(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, allowLocalBuilds)
	if err != nil {
		return seam.DeploySpec{}, err
	}
	if target.RemoteRepo() && strings.TrimSpace(spec.Deploy.Version) == "" {
		spec.Deploy.Version = core.NormalizeBuildInfo(buildInfo).Version
	}
	return spec, nil
}

func newRunInitForArgs(store config.OpenStore, runInit func(core.Context, seam.BootstrapInitParams) error) func(core.Context, []string) error {
	return func(ctx core.Context, args []string) error {
		params, err := inittarget.InitParamsForOpenArgs(store, args)
		if err != nil {
			return err
		}
		return runInit(ctx, params)
	}
}

func newRunInitForOpen(store config.OpenStore, runInit func(core.Context, seam.BootstrapInitParams) error) func(core.Context, model.OpenParams) error {
	return func(ctx core.Context, params model.OpenParams) error {
		initParams, err := inittarget.InitParamsForOpenTarget(store, params)
		if err != nil {
			return err
		}
		return runInit(ctx, initParams)
	}
}

func newPushOperation(pushDockerImage seam.DockerImagePusherFunc, loginToDockerRegistry seam.DockerRegistryLoginFunc, selectRunner prompt.SelectRunner) seam.DockerPushFunc {
	return func(ctx core.Context, pushInput model.DockerPushSpec) error {
		return build.RunDockerPushWithRetry(
			ctx,
			pushInput,
			func(ctx core.Context, pushInput model.DockerPushSpec) error {
				return push.RunDockerPush(ctx, pushInput, pushDockerImage)
			},
			loginToDockerRegistry,
			selectRunner,
		)
	}
}

func hasOptionalBuildCmd(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc) bool {
	hasScript, err := script.HasProjectBuildScript(findProjectRoot, model.DockerCommandTarget{})
	if err == nil && hasScript {
		return true
	}

	linuxContexts, err := linuxpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{})
	if err == nil && len(linuxContexts) > 0 {
		return true
	}

	buildContexts, err := buildspec.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{})
	if err != nil {
		// A misconfigured build setup is a hard error that `erun build` must surface; only not-found means no build.
		return !errors.Is(err, dockerspec.ErrDockerBuildContextNotFound)
	}
	return len(buildContexts) > 0
}

func hasOptionalPushCmd(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc) bool {
	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return true
	}
	// push attaches wherever build does so the two stay symmetric.
	buildContexts, contextErr := buildspec.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{})
	if contextErr != nil {
		return !errors.Is(contextErr, dockerspec.ErrDockerBuildContextNotFound)
	}
	return len(buildContexts) > 0
}

// buildShortTarget honours an env's build.sh opt-out in the build command's short help.
func buildShortTarget(store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc) model.DockerCommandTarget {
	target := model.DockerCommandTarget{}
	if env := buildspec.ResolveDockerBuildEnvConfig(store, findProjectRoot, target); env != nil && env.DisableBuildScript {
		target.DisableBuildScriptDiscovery = true
	}
	return target
}

func optionalBuildCmdShort(store seam.DockerStore, findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc) string {
	target := buildShortTarget(store, findProjectRoot)

	hasRootScript, err := hasProjectRootBuildScript(findProjectRoot, target.DisableBuildScriptDiscovery)
	if err == nil && hasRootScript {
		return "Build the project"
	}

	if hasLinuxPackageContexts(findProjectRoot, resolveBuildContext) {
		return "Build the project"
	}

	buildContexts, err := buildspec.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err == nil && len(buildContexts) > 0 {
		return dockerBuildCmdShort(findProjectRoot, resolveBuildContext, buildContexts)
	}

	hasScript, err := script.HasProjectBuildScript(findProjectRoot, target)
	if err == nil && hasScript {
		return "Build the project"
	}
	if err != nil {
		return "Build the project's container images"
	}

	return "Build the project's container images"
}

func hasLinuxPackageContexts(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc) bool {
	linuxContexts, err := linuxpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{})
	return err == nil && len(linuxContexts) > 0
}

func dockerBuildCmdShort(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc, buildContexts []model.DockerBuildContext) string {
	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" && len(buildContexts) == 1 {
		return "Build the project's container images"
	}
	if currentBuildContextIsProjectRoot(findProjectRoot, buildContext) {
		return "Build and push the project"
	}
	return "Build and push the devops container images for the current project"
}

func currentBuildContextIsProjectRoot(findProjectRoot seam.ProjectFinderFunc, buildContext model.DockerBuildContext) bool {
	projectRoot, err := projectRootForHelp(findProjectRoot)
	return err == nil && projectRoot != "" && filepath.Clean(strings.TrimSpace(buildContext.Dir)) == projectRoot
}

func hasProjectRootBuildScript(findProjectRoot seam.ProjectFinderFunc, disabled bool) (bool, error) {
	if disabled {
		return false, nil
	}
	projectRoot, err := projectRootForHelp(findProjectRoot)
	if err != nil || projectRoot == "" {
		return false, err
	}
	info, err := os.Stat(filepath.Join(projectRoot, "build.sh"))
	if err == nil {
		return !info.IsDir(), nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func optionalPushCmdShort(findProjectRoot seam.ProjectFinderFunc, resolveBuildContext seam.BuildContextResolverFunc) string {
	buildContexts, err := buildspec.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, model.DockerCommandTarget{})
	if err != nil {
		return "Build and push the current container image"
	}

	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" && len(buildContexts) == 1 {
		return "Build and push the current container image"
	}

	if len(buildContexts) > 0 {
		return "Build and push the devops container images for the current project"
	}

	return "Build and push the current container image"
}

func projectRootForHelp(findProjectRoot seam.ProjectFinderFunc) (string, error) {
	if findProjectRoot == nil {
		findProjectRoot = config.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, config.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(projectRoot)), nil
}
