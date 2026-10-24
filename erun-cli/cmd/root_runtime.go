package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/cmd/build"
	"github.com/sophium/erun/cmd/initcmd"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/command/setup"
	"github.com/sophium/erun/erun-common/infra/kube"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	"github.com/sophium/erun/erun-common/primitive/push"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

type rootStore interface {
	setup.BootstrapStore
	config.OpenStore
	sharedpkg.DockerStore
	deploy.DeployStore
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
		PersistentPreRun: shared.AuditCommand,
		RunE:             runRoot,
	}
	shared.AddDryRunFlag(cmd)
	shared.AddTimeFlag(cmd)
	shared.AddOutputFlag(cmd)
	shared.WrapCommandTreeWithElapsedTime(cmd)
	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", shared.VerboseFlagUsage)
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
	var unavailable setup.TenantSelectionUnavailableError
	if errors.As(err, &unavailable) {
		return internal.WithExitCode(err, tenantSelectionUnavailableExitCode)
	}
	return err
}

func newRunInit(store setup.BootstrapStore, findProjectRoot sharedpkg.ProjectFinderFunc, promptRunner shared.PromptRunner, selectRunner shared.SelectRunner, listKubernetesContexts shared.KubernetesContextsLister, ensureKubernetesNamespace deploy.NamespaceEnsurerFunc, waitForRemoteRuntime setup.RemoteRuntimeWaitFunc, runRemoteCommand kube.RemoteCommandRunnerFunc, deployHelmChart deploy.HelmChartDeployerFunc) func(core.Context, open.BootstrapInitParams) error {
	return func(ctx core.Context, params open.BootstrapInitParams) error {
		ctx = shared.WithCloudContextPreflight(ctx, store)
		params.RuntimeVersionDefault = shared.CurrentBuildInfo().Version
		_, err := setup.RunBootstrapInitWithDependencies(setup.BootstrapInitDependencies{
			Store:           setup.TraceBootstrapStore(ctx, store),
			FindProjectRoot: findProjectRoot,
			SelectTenant: func(tenants []config.TenantConfig) (setup.TenantSelectionResult, error) {
				return initcmd.SelectTenantPrompt(selectRunner, tenants)
			},
			TenantSelectionUnavailable: initcmd.TenantSelectionUnavailable(),
			Confirm: func(label string) (bool, error) {
				return shared.ConfirmPrompt(promptRunner, label)
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

func resolveRuntimeDeploySpecForOpen(ctx core.Context, store deploy.DeployStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveDockerBuildContext sharedpkg.BuildContextResolverFunc, resolveKubernetesDeployContext deploy.DeployContextResolverFunc, now sharedpkg.NowFunc, buildInfo model.BuildInfo, target config.OpenResult, allowLocalBuilds bool) (deploy.DeploySpec, error) {
	spec, err := deploy.ResolveOpenRuntimeDeploySpec(ctx, store, findProjectRoot, resolveDockerBuildContext, resolveKubernetesDeployContext, now, target, allowLocalBuilds)
	if err != nil {
		return deploy.DeploySpec{}, err
	}
	if target.RemoteRepo() && strings.TrimSpace(spec.Deploy.Version) == "" {
		spec.Deploy.Version = model.NormalizeBuildInfo(buildInfo).Version
	}
	return spec, nil
}

func newRunInitForArgs(store config.OpenStore, runInit func(core.Context, open.BootstrapInitParams) error) func(core.Context, []string) error {
	return func(ctx core.Context, args []string) error {
		params, err := open.InitParamsForOpenArgs(store, args)
		if err != nil {
			return err
		}
		return runInit(ctx, params)
	}
}

func newRunInitForOpen(store config.OpenStore, runInit func(core.Context, open.BootstrapInitParams) error) func(core.Context, sharedpkg.OpenParams) error {
	return func(ctx core.Context, params sharedpkg.OpenParams) error {
		initParams, err := open.InitParamsForOpenTarget(store, params)
		if err != nil {
			return err
		}
		return runInit(ctx, initParams)
	}
}

func newPushOperation(pushDockerImage sharedpkg.DockerImagePusherFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner) buildpkg.DockerPushFunc {
	return func(ctx core.Context, pushInput sharedpkg.DockerPushSpec) error {
		return build.RunDockerPushWithRetry(
			ctx,
			pushInput,
			func(ctx core.Context, pushInput sharedpkg.DockerPushSpec) error {
				return push.RunDockerPush(ctx, pushInput, pushDockerImage)
			},
			loginToDockerRegistry,
			selectRunner,
		)
	}
}

func hasOptionalBuildCmd(findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc) bool {
	hasScript, err := buildpkg.HasProjectBuildScript(findProjectRoot, sharedpkg.DockerCommandTarget{})
	if err == nil && hasScript {
		return true
	}

	linuxContexts, err := buildpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, sharedpkg.DockerCommandTarget{})
	if err == nil && len(linuxContexts) > 0 {
		return true
	}

	buildContexts, err := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, sharedpkg.DockerCommandTarget{})
	if err != nil {
		// A misconfigured build setup is a hard error that `erun build` must surface; only not-found means no build.
		return !errors.Is(err, sharedpkg.ErrDockerBuildContextNotFound)
	}
	return len(buildContexts) > 0
}

func hasOptionalPushCmd(findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc) bool {
	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" {
		return true
	}
	// push attaches wherever build does so the two stay symmetric.
	buildContexts, contextErr := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, sharedpkg.DockerCommandTarget{})
	if contextErr != nil {
		return !errors.Is(contextErr, sharedpkg.ErrDockerBuildContextNotFound)
	}
	return len(buildContexts) > 0
}

// buildShortTarget honours an env's build.sh opt-out in the build command's short help.
func buildShortTarget(store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc) sharedpkg.DockerCommandTarget {
	target := sharedpkg.DockerCommandTarget{}
	if env := buildpkg.ResolveDockerBuildEnvConfig(store, findProjectRoot, target); env != nil && env.DisableBuildScript {
		target.DisableBuildScriptDiscovery = true
	}
	return target
}

func optionalBuildCmdShort(store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc) string {
	target := buildShortTarget(store, findProjectRoot)

	hasRootScript, err := hasProjectRootBuildScript(findProjectRoot, target.DisableBuildScriptDiscovery)
	if err == nil && hasRootScript {
		return "Build the project"
	}

	if hasLinuxPackageContexts(findProjectRoot, resolveBuildContext) {
		return "Build the project"
	}

	buildContexts, err := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, target)
	if err == nil && len(buildContexts) > 0 {
		return dockerBuildCmdShort(findProjectRoot, resolveBuildContext, buildContexts)
	}

	hasScript, err := buildpkg.HasProjectBuildScript(findProjectRoot, target)
	if err == nil && hasScript {
		return "Build the project"
	}
	if err != nil {
		return "Build the project's container images"
	}

	return "Build the project's container images"
}

func hasLinuxPackageContexts(findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc) bool {
	linuxContexts, err := buildpkg.ResolveCurrentLinuxPackageContexts(findProjectRoot, resolveBuildContext, sharedpkg.DockerCommandTarget{})
	return err == nil && len(linuxContexts) > 0
}

func dockerBuildCmdShort(findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, buildContexts []sharedpkg.DockerBuildContext) string {
	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" && len(buildContexts) == 1 {
		return "Build the project's container images"
	}
	if currentBuildContextIsProjectRoot(findProjectRoot, buildContext) {
		return "Build and push the project"
	}
	return "Build and push the devops container images for the current project"
}

func currentBuildContextIsProjectRoot(findProjectRoot sharedpkg.ProjectFinderFunc, buildContext sharedpkg.DockerBuildContext) bool {
	projectRoot, err := projectRootForHelp(findProjectRoot)
	return err == nil && projectRoot != "" && filepath.Clean(strings.TrimSpace(buildContext.Dir)) == projectRoot
}

func hasProjectRootBuildScript(findProjectRoot sharedpkg.ProjectFinderFunc, disabled bool) (bool, error) {
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

func optionalPushCmdShort(findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc) string {
	buildContexts, err := buildpkg.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, sharedpkg.DockerCommandTarget{})
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

func projectRootForHelp(findProjectRoot sharedpkg.ProjectFinderFunc) (string, error) {
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
