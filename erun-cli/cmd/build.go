package cmd

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

const (
	loginAndRetryPushOption = "Login and retry push"
	cancelPushOption        = "Cancel"
)

var errVersionFileNotFound = common.ErrVersionFileNotFound

func newBuildCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	cmd := &cobra.Command{
		Use:           "build",
		Short:         "Build the container image in the current directory",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runBuildCommand(commandContext(cmd), store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addBuildCommandTargetFlags(cmd, &target)
	return cmd
}

func runBuildCommand(ctx common.Context, store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, target common.DockerCommandTarget, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) error {
	execution, err := common.ResolveBuildExecution(store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil {
		return err
	}
	buildWithRetry := func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
		return runDockerBuildWithRetry(
			ctx,
			buildInput,
			func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
				if buildDockerImage == nil {
					return common.DockerImageBuilder(buildInput, stdout, stderr)
				}
				return buildDockerImage(buildInput, stdout, stderr)
			},
			loginToDockerRegistry,
			selectRunner,
			stdout,
			stderr,
		)
	}
	if !target.Deploy {
		return common.RunBuildExecution(ctx, execution, runBuildScript, buildWithRetry, push)
	}
	if common.BuildExecutionUsesBuildScript(execution) {
		return errors.New("build deploy is not supported for project build scripts")
	}

	buildDeployStore, ok := any(store).(common.BuildDeployStore)
	if !ok {
		return errors.New("store does not support deploy resolution")
	}

	deploySpecs, err := common.ResolveCurrentDeploySpecsForDockerTarget(buildDeployStore, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target)
	if err != nil {
		return err
	}

	return common.RunBuildExecutionAndDeploy(ctx, execution, deploySpecs, runBuildScript, buildWithRetry, push, deployHelmChart)
}

func newPushCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	var force bool
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Build and push the current container image",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force {
				target.NoIncremental = true
			}
			ctx := commandContext(cmd)
			pushInput, buildInput, err := common.ResolveDockerPushSpec(store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			builder := buildDockerImage
			if builder == nil {
				builder = common.DockerImageBuilder
			}
			builderWithGuidance := func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
				buildErr := builder(buildInput, stdout, stderr)
				var authErr common.DockerRegistryAuthError
				if !errors.As(buildErr, &authErr) {
					return buildErr
				}
				if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return builder(buildInput, stdout, stderr) }, stdout, stderr); handled {
					return finalErr
				}
				return buildErr
			}
			return common.RunDockerPushSpec(ctx, pushInput, buildInput, builderWithGuidance, push)
		},
	}
	addDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache")
	return cmd
}

// newRootPushCmd is the top-level "erun push" shorthand. It supports both
// single-image push (when a Dockerfile exists in the current directory) and
// multi-image push (when run from the project root with multiple docker
// contexts). The nested "devops container push" command uses newPushCmd which
// is single-image only.
func newRootPushCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	var force bool
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Build and push the current container image",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force {
				target.NoIncremental = true
			}
			ctx := commandContext(cmd)
			builder := buildDockerImage
			if builder == nil {
				builder = common.DockerImageBuilder
			}
			builderWithGuidance := func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
				buildErr := builder(buildInput, stdout, stderr)
				var authErr common.DockerRegistryAuthError
				if !errors.As(buildErr, &authErr) {
					return buildErr
				}
				if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return builder(buildInput, stdout, stderr) }, stdout, stderr); handled {
					return finalErr
				}
				return buildErr
			}
			buildContext, _ := resolveBuildContext()
			if strings.TrimSpace(buildContext.DockerfilePath) != "" {
				pushInput, buildInput, err := common.ResolveDockerPushSpec(store, findProjectRoot, resolveBuildContext, now, target)
				if err != nil {
					return err
				}
				return common.RunDockerPushSpec(ctx, pushInput, buildInput, builderWithGuidance, push)
			}
			execution, err := common.ResolveDockerPushExecution(store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			return common.RunDockerPushExecution(ctx, execution, builderWithGuidance, push)
		},
	}
	addDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache")
	return cmd
}

func runDockerPushWithRetry(ctx common.Context, pushInput common.DockerPushSpec, push common.DockerPushFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner) error {
	err := push(ctx, pushInput)
	if err == nil {
		return nil
	}

	var authErr common.DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return err
	}

	if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return push(ctx, pushInput) }, ctx.Stdout, ctx.Stderr); handled {
		return finalErr
	}

	retry, promptErr := promptDockerLoginRetry(selectRunner, authErr.Registry)
	if promptErr != nil {
		return promptErr
	}
	if !retry {
		return err
	}

	loginArgs := []string{"login"}
	if strings.TrimSpace(authErr.Registry) != "" {
		loginArgs = append(loginArgs, authErr.Registry)
	}
	ctx.TraceCommand(pushInput.Dir, "docker", loginArgs...)
	if loginErr := loginToDockerRegistry(authErr.Registry, ctx.Stdin, ctx.Stdout, ctx.Stderr); loginErr != nil {
		return loginErr
	}

	return push(ctx, pushInput)
}

func runDockerBuildWithRetry(ctx common.Context, buildInput common.DockerBuildSpec, build common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, stdout, stderr io.Writer) error {
	err := build(buildInput, stdout, stderr)
	if err == nil {
		return nil
	}

	var authErr common.DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return err
	}

	if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return build(buildInput, stdout, stderr) }, stdout, stderr); handled {
		return finalErr
	}

	retry, promptErr := promptDockerLoginRetry(selectRunner, authErr.Registry)
	if promptErr != nil {
		return promptErr
	}
	if !retry {
		return err
	}

	loginArgs := []string{"login"}
	if strings.TrimSpace(authErr.Registry) != "" {
		loginArgs = append(loginArgs, authErr.Registry)
	}
	ctx.TraceCommand(buildInput.ContextDir, "docker", loginArgs...)
	if loginErr := loginToDockerRegistry(authErr.Registry, ctx.Stdin, ctx.Stdout, ctx.Stderr); loginErr != nil {
		return loginErr
	}

	return build(buildInput, stdout, stderr)
}

func addBuildCommandTargetFlags(cmd *cobra.Command, target *common.DockerCommandTarget) {
	addPushCommandTargetFlags(cmd, target)
	cmd.Flags().BoolVar(&target.Deploy, "deploy", false, "Deploy the built version after the build completes")
	cmd.Flags().BoolVar(&target.Release, "release", false, "Run release first and publish the release-tagged images")
	cmd.Flags().BoolVar(&target.Force, "force", false, "Delete and recreate conflicting release tags when combined with --release")
	cmd.Flags().BoolVar(&target.NoIncremental, "no-incremental", false, "Disable fingerprint-based build caching and rebuild every image from scratch")
}

func addPushCommandTargetFlags(cmd *cobra.Command, target *common.DockerCommandTarget) {
	cmd.Flags().StringVar(&target.ProjectRoot, "project-root", "", "Project root override for internal tooling")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Environment override for internal tooling")
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Override the resolved image version")
	_ = cmd.Flags().MarkHidden("project-root")
	_ = cmd.Flags().MarkHidden("environment")
}

// handleNamespaceAuthError handles the create_package and scope-denied auth
// errors by switching docker auth to the namespace owner via the gh CLI and
// retrying. Returns (handled=true, finalErr=...) when the error matched one
// of those cases — finalErr is nil on a successful retry, or the latest
// error otherwise. Returns (false, nil) when the error did not match, in
// which case the caller should fall through to the prompt-login path.
func handleNamespaceAuthError(authErr common.DockerRegistryAuthError, retry func() error, stdout, stderr io.Writer) (bool, error) {
	if !common.IsDockerCreatePackageDenied(authErr.Message) && !common.IsDockerScopeDenied(authErr.Message) {
		return false, nil
	}
	var finalErr error
	if ok, _ := common.TryGHCRNamespaceLogin(authErr.Tag, stdout, stderr); ok {
		if retryErr := retry(); retryErr == nil {
			return true, nil
		} else {
			finalErr = retryErr
		}
	}
	if common.IsDockerCreatePackageDenied(authErr.Message) {
		printCreatePackageGuidance(stderr, authErr.Tag, authErr.Registry)
	}
	return true, finalErr
}

func printCreatePackageGuidance(out io.Writer, tag, registry string) {
	if out == nil {
		return
	}
	namespace := common.DockerNamespaceFromTag(tag)
	registryHost := strings.TrimSpace(registry)
	if registryHost == "" {
		registryHost = "the registry"
	}

	var sb strings.Builder
	sb.WriteString("\n")
	fmt.Fprintf(&sb, "%s rejected the push: only the namespace owner can create new packages under %s.\n\n", registryHost, namespacePath(registryHost, namespace))

	if isGHCR(registryHost) {
		owner := namespace
		if owner == "" {
			owner = "<owner>"
		}
		fmt.Fprintf(&sb, "To bootstrap the first version of this image, get a personal access token from the GitHub account that owns ghcr.io/%s/:\n", owner)
		sb.WriteString("  1. Sign into github.com as that account.\n")
		sb.WriteString("  2. Open https://github.com/settings/tokens/new (classic).\n")
		sb.WriteString("  3. Generate a token with scopes: write:packages and read:packages.\n")
		sb.WriteString("  4. docker logout ghcr.io\n")
		fmt.Fprintf(&sb, "  5. echo $TOKEN | docker login ghcr.io -u %s --password-stdin\n", owner)
		sb.WriteString("  6. Re-run erun push.\n\n")
		sb.WriteString("After the package exists the owner can grant Write access to others (per-package settings, or via \"Inherit access from source repository\" on a linked repo). Future versions can then be pushed by anyone with that access — no PAT needed.\n")
	} else {
		sb.WriteString("Obtain credentials from the namespace owner or registry administrator and run:\n")
		fmt.Fprintf(&sb, "  docker logout %s && docker login %s\n", registryHost, registryHost)
		sb.WriteString("Then re-run erun push.\n")
	}
	sb.WriteString("\n")
	_, _ = io.WriteString(out, sb.String())
}

func namespacePath(registry, namespace string) string {
	registry = strings.TrimSpace(registry)
	namespace = strings.TrimSpace(namespace)
	if namespace == "" {
		return registry + "/"
	}
	if registry == "" {
		return namespace + "/"
	}
	return registry + "/" + namespace + "/"
}

func isGHCR(registry string) bool {
	registry = strings.ToLower(strings.TrimSpace(registry))
	return registry == "ghcr.io" || strings.HasPrefix(registry, "ghcr.io/")
}

func promptDockerLoginRetry(run SelectRunner, registry string) (bool, error) {
	label := fmt.Sprintf("Docker push requires login to %s", common.DockerRegistryDisplayName(registry))
	prompt := promptui.Select{
		Label: label,
		Items: []string{loginAndRetryPushOption, cancelPushOption},
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("docker login selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}

	return result == loginAndRetryPushOption, nil
}
