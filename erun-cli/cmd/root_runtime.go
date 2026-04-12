package cmd

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/spf13/cobra"
)

type rootStore interface {
	common.BootstrapStore
	common.OpenStore
	common.DockerStore
	common.DeployStore
}

func addCommands(parent *cobra.Command, commands ...*cobra.Command) {
	for _, command := range commands {
		if command == nil {
			continue
		}
		parent.AddCommand(command)
	}
}

func newCommandGroup(use, short string, commands ...*cobra.Command) *cobra.Command {
	command := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	addCommands(command, commands...)
	return command
}

func newRootCommand(runRoot func(*cobra.Command, []string) error) *cobra.Command {
	var verbosity int

	cmd := &cobra.Command{
		Use:              "erun",
		Short:            "Environment Runner",
		Long:             "erun helps to run and manage multiple tenants/environments.\n\nVerbosity levels:\n  -v    print trace logs for command flow and side effects\n\nDry-run:\n  --dry-run runs the same resolution flow but skips mutating operations",
		Example:          "  erun deploy --dry-run\n  erun -v deploy --dry-run\n  erun -vv init -y\n  eval \"$(erun open --no-shell)\"",
		Args:             cobra.MaximumNArgs(2),
		SilenceUsage:     true,
		SilenceErrors:    true,
		TraverseChildren: true,
		RunE:             runRoot,
	}
	addDryRunFlag(cmd)
	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", verboseFlagUsage)
	return cmd
}

func newRunInit(store common.BootstrapStore, findProjectRoot common.ProjectFinderFunc, promptRunner PromptRunner, selectRunner SelectRunner, listKubernetesContexts KubernetesContextsLister, ensureKubernetesNamespace common.NamespaceEnsurerFunc) func(common.Context, common.BootstrapInitParams) error {
	return func(ctx common.Context, params common.BootstrapInitParams) error {
		if strings.TrimSpace(params.RuntimeVersion) == "" {
			params.RuntimeVersion = currentBuildInfo().Version
		}
		_, err := common.RunBootstrapInit(
			ctx,
			params,
			common.TraceBootstrapStore(ctx, store),
			findProjectRoot,
			nil,
			func(tenants []common.TenantConfig) (common.TenantSelectionResult, error) {
				return selectTenantPrompt(selectRunner, tenants)
			},
			func(label string) (bool, error) {
				return confirmPrompt(promptRunner, label)
			},
			func(label string) (string, error) {
				return kubernetesContextPrompt(promptRunner, selectRunner, listKubernetesContexts, label)
			},
			func(label string) (string, error) {
				return containerRegistryPrompt(promptRunner, label)
			},
			common.TraceNamespaceEnsurer(ctx, ensureKubernetesNamespace),
			common.LoadProjectConfig,
			common.TraceProjectConfigSaver(ctx, common.SaveProjectConfig),
		)
		if err != nil {
			if errors.Is(err, common.ErrNotInGitRepository) {
				return internal.MarkReported(common.ErrNotInGitRepository)
			}
			return err
		}
		return nil
	}
}

func newRunInitForArgs(store common.OpenStore, runInit func(common.Context, common.BootstrapInitParams) error) func(common.Context, []string) error {
	return func(ctx common.Context, args []string) error {
		params, err := common.InitParamsForOpenArgs(store, args)
		if err != nil {
			return err
		}
		return runInit(ctx, params)
	}
}

func newPushOperation(pushDockerImage common.DockerImagePusherFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner) common.DockerPushFunc {
	return func(ctx common.Context, pushInput common.DockerPushSpec) error {
		return runDockerPushWithRetry(
			ctx,
			pushInput,
			func(ctx common.Context, pushInput common.DockerPushSpec) error {
				return common.RunDockerPush(ctx, pushInput, pushDockerImage)
			},
			loginToDockerRegistry,
			selectRunner,
		)
	}
}

func hasOptionalBuildCmd(findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc) bool {
	hasScript, err := common.HasProjectBuildScript(findProjectRoot, common.DockerCommandTarget{})
	if err == nil && hasScript {
		return true
	}

	buildContexts, err := common.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, common.DockerCommandTarget{})
	return err == nil && len(buildContexts) > 0
}

func hasOptionalPushCmd(findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc) bool {
	buildContext, err := resolveBuildContext()
	return err == nil && strings.TrimSpace(buildContext.DockerfilePath) != ""
}

func optionalBuildCmdShort(findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc) string {
	hasScript, err := common.HasProjectBuildScript(findProjectRoot, common.DockerCommandTarget{})
	if err == nil && hasScript {
		return "Build the project"
	}

	buildContexts, err := common.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, common.DockerCommandTarget{})
	if err != nil {
		return "Build the container image in the current directory"
	}

	buildContext, err := resolveBuildContext()
	if err == nil && strings.TrimSpace(buildContext.DockerfilePath) != "" && len(buildContexts) == 1 {
		return "Build the container image in the current directory"
	}

	if len(buildContexts) > 0 {
		if projectRoot, projectRootErr := projectRootForHelp(findProjectRoot); projectRootErr == nil &&
			projectRoot != "" &&
			filepath.Clean(strings.TrimSpace(buildContext.Dir)) == projectRoot {
			return "Build and push the project"
		}
		return "Build and push the devops container images for the current project"
	}

	return "Build the container image in the current directory"
}

func optionalPushCmdShort(findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc) string {
	buildContexts, err := common.ResolveCurrentDockerBuildContexts(findProjectRoot, resolveBuildContext, common.DockerCommandTarget{})
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

func projectRootForHelp(findProjectRoot common.ProjectFinderFunc) (string, error) {
	if findProjectRoot == nil {
		findProjectRoot = common.FindProjectRoot
	}
	_, projectRoot, err := findProjectRoot()
	if err != nil {
		if errors.Is(err, common.ErrNotInGitRepository) {
			return "", nil
		}
		return "", err
	}
	return filepath.Clean(strings.TrimSpace(projectRoot)), nil
}

func hasOptionalDeployCmd(resolveDeployContext common.DeployContextResolverFunc) bool {
	if resolveDeployContext == nil {
		return false
	}

	deployContext, err := resolveDeployContext()
	if err != nil {
		return false
	}
	if strings.TrimSpace(deployContext.ChartPath) != "" {
		return true
	}
	if deployContexts, err := common.ResolveKubernetesDeployContextsAtDir(deployContext.Dir); err == nil && len(deployContexts) > 0 {
		return true
	}
	k8sDir := filepath.Join(strings.TrimSpace(deployContext.Dir), "k8s")
	if deployContexts, err := common.ResolveKubernetesDeployContextsAtDir(k8sDir); err == nil && len(deployContexts) > 0 {
		return true
	}

	entries, err := os.ReadDir(strings.TrimSpace(deployContext.Dir))
	if err != nil {
		return false
	}
	found := false
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasSuffix(entry.Name(), "-devops") {
			continue
		}
		k8sDir := filepath.Join(strings.TrimSpace(deployContext.Dir), entry.Name(), "k8s")
		if deployContexts, err := common.ResolveKubernetesDeployContextsAtDir(k8sDir); err == nil && len(deployContexts) > 0 {
			if found {
				return false
			}
			found = true
		}
	}
	return found
}
