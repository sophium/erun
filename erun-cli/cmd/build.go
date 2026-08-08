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

var errPushVersionRequired = fmt.Errorf("push requires a version: pass --version <version> produced by `erun build`. push publishes a built version's images and chart; it does not mint a version")

var errPushBuildVersionConflict = fmt.Errorf("push --build builds and pushes the version it mints; do not also pass --version")

func newBuildCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	var jobs int
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the project's container images",
		Long: "Build the project's container images.\n\n" +
			"The build step of the build → release → push → deploy flow. Builds locally " +
			"without publishing by default; --release stamps and publishes the release version " +
			"first, and --deploy pushes the images and rolls them out — folding the later steps " +
			"into one command so the version flows through for you.",
		Example:       "  erun build\n  erun build --release\n  erun build --release --deploy",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			ctx.BuildJobs = jobs
			return runBuildCommand(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, deployHelmChart)
		},
	}
	addDryRunFlag(cmd)
	addBuildCommandTargetFlags(cmd, &target)
	// Only on build. push and release also call addPushCommandTargetFlags, and
	// their builds stay sequential, so offering the knob there would advertise a
	// control that does nothing.
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 0, "Build this many images at once (0 resolves from the machine, 1 is sequential). Independent images build concurrently; a FROM dependency still waits for its base.")
	return cmd
}

func runBuildCommand(ctx common.Context, store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, resolveDeployContext common.DeployContextResolverFunc, now common.NowFunc, target common.DockerCommandTarget, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc, deployHelmChart common.HelmChartDeployerFunc) error {
	execution, err := common.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, target)
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
		if err := common.RunBuildExecution(ctx, execution, runBuildScript, buildWithRetry, push); err != nil {
			return err
		}
		return ctx.WriteResult(common.NewBuildResult(execution))
	}
	if common.BuildExecutionUsesBuildScript(execution) {
		return errors.New("build deploy is not supported for project build scripts")
	}

	buildDeployStore, ok := any(store).(common.BuildDeployStore)
	if !ok {
		return errors.New("store does not support deploy resolution")
	}

	deploySpecs, err := common.ResolveCurrentDeploySpecsForDockerTarget(ctx, buildDeployStore, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target)
	if err != nil {
		return err
	}

	if err := common.RunBuildExecutionAndDeploy(ctx, execution, deploySpecs, runBuildScript, buildWithRetry, push, deployHelmChart); err != nil {
		return err
	}
	return ctx.WriteResult(common.NewBuildResult(execution))
}

func newPushCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	var force bool
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Publish a built container image at a version",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force {
				target.NoIncremental = true
			}
			if strings.TrimSpace(target.VersionOverride) == "" {
				return errPushVersionRequired
			}
			ctx := commandContext(cmd)
			pushInput, buildInput, err := common.ResolveDockerPushSpec(ctx, store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
			return common.RunPushCommand(ctx, func() error {
				return common.RunDockerPushSpec(ctx, pushInput, buildInput, buildWithRetry, push)
			})
		},
	}
	addDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to publish, produced by erun build")
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache")
	return cmd
}

func pushBuildWithRetry(ctx common.Context, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner) common.DockerImageBuilderFunc {
	return func(buildInput common.DockerBuildSpec, stdout, stderr io.Writer) error {
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
}

// newRootPushCmd is the top-level `erun push`. --build is an operator shortcut
// that builds first; that build→push orchestration deliberately lives in this
// caller so push itself stays a pure primitive publishing an explicit version.
func newRootPushCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	var force bool
	cmd := &cobra.Command{
		Use:   "push",
		Short: "Publish a built version's container images and chart to the registry",
		Long: "Publish a version's container images and runtime chart to the registry — the " +
			"push step of the build → release → push → deploy flow.\n\n" +
			"A version is required (--version <v>, the same flag deploy uses) and is the one " +
			"`erun build` minted; push publishes it, it does not mint one. push resolves that " +
			"version's images from the local source (promoting unchanged images from the " +
			"fingerprint cache, rebuilding only what changed), then pushes the multi-arch " +
			"manifest and, for the runtime image, the runtime chart alongside it.\n\n" +
			"--build is an operator shortcut that builds the current source first and then " +
			"pushes the version that build mints, so you don't have to copy the snapshot " +
			"version out of `erun build` by hand. It is mutually exclusive with --version.",
		Example:       "  erun push --version 1.2.3-snapshot-20260101010101\n  erun push --build",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force {
				target.NoIncremental = true
			}
			ctx := commandContext(cmd)
			if target.Build {
				if strings.TrimSpace(target.VersionOverride) != "" {
					return errPushBuildVersionConflict
				}
				if err := runPushBuildStep(ctx, store, findProjectRoot, resolveBuildContext, now, &target, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push); err != nil {
					return err
				}
			} else if strings.TrimSpace(target.VersionOverride) == "" {
				return errPushVersionRequired
			}
			buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
			buildContext, _ := resolveBuildContext()
			if strings.TrimSpace(buildContext.DockerfilePath) != "" {
				pushInput, buildInput, err := common.ResolveDockerPushSpec(ctx, store, findProjectRoot, resolveBuildContext, now, target)
				if err != nil {
					return err
				}
				return common.RunPushCommand(ctx, func() error {
					return common.RunDockerPushSpec(ctx, pushInput, buildInput, buildWithRetry, push)
				})
			}
			execution, err := common.ResolveDockerPushExecution(ctx, store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			return common.RunPushCommand(ctx, func() error {
				return common.RunDockerPushExecution(ctx, execution, buildWithRetry, push)
			})
		},
	}
	addDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to publish, produced by erun build")
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache (also forces the --build step)")
	cmd.Flags().BoolVar(&target.Build, "build", false, "Build the current source first, then push the version it mints (operator shortcut for build → push)")
	return cmd
}

func runPushBuildStep(ctx common.Context, store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, target *common.DockerCommandTarget, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, loginToDockerRegistry common.DockerRegistryLoginFunc, selectRunner SelectRunner, push common.DockerPushFunc) error {
	execution, err := common.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, *target)
	if err != nil {
		return err
	}
	buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
	if err := common.RunBuildExecution(ctx, execution, runBuildScript, buildWithRetry, push); err != nil {
		return err
	}
	version := strings.TrimSpace(common.NewBuildResult(execution).Version)
	if version == "" {
		return errors.New("erun push --build: the build did not mint a version to push")
	}
	target.VersionOverride = version
	return nil
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

	if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return push(ctx, pushInput) }, ctx.Stdin, ctx.Stdout, ctx.Stderr); handled {
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

	if handled, finalErr := handleNamespaceAuthError(authErr, func() error { return build(buildInput, stdout, stderr) }, ctx.Stdin, stdout, stderr); handled {
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
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Override the version build would mint")
	cmd.Flags().BoolVar(&target.Deploy, "deploy", false, "Deploy the built version after the build completes")
	cmd.Flags().BoolVar(&target.Release, "release", false, "Run release first and publish the release-tagged images")
	cmd.Flags().BoolVar(&target.Force, "force", false, "Delete and recreate conflicting release tags when combined with --release")
	cmd.Flags().BoolVar(&target.NoIncremental, "no-incremental", false, "Disable fingerprint-based build caching and rebuild every image from scratch")
}

func addPushCommandTargetFlags(cmd *cobra.Command, target *common.DockerCommandTarget) {
	cmd.Flags().StringVar(&target.ProjectRoot, "project-root", "", "Project root override for internal tooling")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Environment override for internal tooling")
	_ = cmd.Flags().MarkHidden("project-root")
	_ = cmd.Flags().MarkHidden("environment")
}

// handleNamespaceAuthError recovers a GHCR create-package or scope-denied push
// by re-authenticating as the namespace owner, escalating to an interactive
// `gh auth refresh` when the stored token still lacks the scope. That
// escalation needs a one-time device code, so in a headless runtime pod it
// cannot complete and surfaces an actionable error instead.
func handleNamespaceAuthError(authErr common.DockerRegistryAuthError, retry func() error, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !common.IsDockerCreatePackageDenied(authErr.Message) && !common.IsDockerScopeDenied(authErr.Message) {
		return false, nil
	}
	finalErr := retryAfterNamespaceLogin(authErr, retry, stdout, stderr)
	if finalErr == nil {
		return true, nil
	}
	if scopeStillDenied(finalErr) {
		if refreshErr := retryAfterScopeRefresh(authErr, retry, stdin, stdout, stderr); refreshErr == nil {
			return true, nil
		} else {
			finalErr = refreshErr
		}
	}
	if common.IsDockerCreatePackageDenied(authErr.Message) {
		printCreatePackageGuidance(stderr, authErr.Tag, authErr.Registry)
	}
	return true, finalErr
}

func retryAfterNamespaceLogin(authErr common.DockerRegistryAuthError, retry func() error, stdout, stderr io.Writer) error {
	ok, _ := common.TryGHCRNamespaceLogin(authErr.Tag, stdout, stderr)
	if !ok {
		return authErr
	}
	if retryErr := retry(); retryErr != nil {
		return retryErr
	}
	return nil
}

func retryAfterScopeRefresh(authErr common.DockerRegistryAuthError, retry func() error, stdin io.Reader, stdout, stderr io.Writer) error {
	ok, refreshErr := common.RefreshGHCRPackageScopes(authErr.Tag, stdin, stdout, stderr)
	if refreshErr != nil {
		return refreshErr
	}
	if !ok {
		return authErr
	}
	return retry()
}

func scopeStillDenied(err error) bool {
	var authErr common.DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return false
	}
	return common.IsDockerScopeDenied(authErr.Message)
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
	if isTrueishEnv("ERUN_AUTO_LOGIN_ON_PUSH") {
		return true, nil
	}
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
