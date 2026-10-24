package build

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
	buildrun "github.com/sophium/erun/erun-common/orchestration/build"
	deployrun "github.com/sophium/erun/erun-common/orchestration/deploy"
	"github.com/sophium/erun/erun-common/orchestration/execute"
	buildpkg "github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	pushpkg "github.com/sophium/erun/erun-common/primitive/push"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

const (
	loginAndRetryPushOption = "Login and retry push"
	cancelPushOption        = "Cancel"
)

var errPushVersionRequired = fmt.Errorf("push requires a version: pass --version <version> produced by `erun build`. push publishes a built version's images and chart; it does not mint a version")

var errPushBuildVersionConflict = fmt.Errorf("push --build builds and pushes the version it mints; do not also pass --version")

func NewBuildCmd(store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, resolveDeployContext deploy.DeployContextResolverFunc, now sharedpkg.NowFunc, runBuildScript buildpkg.BuildScriptRunnerFunc, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, push buildpkg.DockerPushFunc, deployHelmChart deploy.HelmChartDeployerFunc, cloudStore config.CloudReadStore, cloudDeps cloud.CloudDependencies) *cobra.Command {
	target := sharedpkg.DockerCommandTarget{}
	var jobs int
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Build the project's container images",
		Long: "Build the project's container images.\n\n" +
			"The build step of the build → release → push → deploy flow. Builds locally " +
			"without publishing by default; --release stamps and publishes the release version " +
			"first, and --deploy pushes the images and rolls them out — folding the later steps " +
			"into one command so the version flows through for you. e2e suites are never part " +
			"of a build: run `erun e2e` against the deployed environment as a separate step.\n\n" +
			"A linux package (<tenant>-devops/linux/<name>/Dockerfile) is never an image: every " +
			"build runs its test stage (its build stage when it has no tests) and exports nothing, " +
			"and --release builds its dist stage for both platforms and attaches the .deb files " +
			"at that stage's root to the GitHub release for the tag, after the tag is pushed.\n\n" +
			"Every platform erun supports builds by default. --platform narrows a local, " +
			"non-release build to only the platform(s) named, so an environment whose cluster " +
			"can only ever run one architecture stops paying for an emulated build of the other; " +
			"the project's .erun/config.yaml docker.platforms pins the same choice permanently, " +
			"project-wide or per environment (an environment opts out of the project default " +
			"with platforms: []). A release build always publishes every platform and refuses " +
			"--platform, since a released artifact must be deployable anywhere.\n\n" +
			"--component selects one of a monorepo's declared .erun/config.yaml components " +
			"entries — a project with more than one independent docker/k8s root (e.g. " +
			"harnesses/<name>/{docker,k8s}) declares one entry per root, and this builds only " +
			"the selected root's images. It auto-selects when exactly one entry is declared and " +
			"is unused (falls through to paths.docker/convention) when the project declares no " +
			"components at all.",
		Example:       "  erun build\n  erun build --release\n  erun build --release --deploy\n  erun build --platform linux/amd64\n  erun build --component ingest-worker",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			ctx.BuildJobs = jobs
			return runBuildCommand(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, deployHelmChart, cloudStore, cloudDeps)
		},
	}
	shared.AddDryRunFlag(cmd)
	addBuildCommandTargetFlags(cmd, &target)
	// Only on build: push and release builds stay sequential.
	cmd.Flags().IntVarP(&jobs, "jobs", "j", 0, "Build this many images at once (0 resolves from the machine, 1 is sequential). Independent images build concurrently; a FROM dependency still waits for its base.")
	cmd.AddCommand(newBuildProfileCmd())
	return cmd
}

func runBuildCommand(ctx core.Context, store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, resolveDeployContext deploy.DeployContextResolverFunc, now sharedpkg.NowFunc, target sharedpkg.DockerCommandTarget, runBuildScript buildpkg.BuildScriptRunnerFunc, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, push buildpkg.DockerPushFunc, deployHelmChart deploy.HelmChartDeployerFunc, cloudStore config.CloudReadStore, cloudDeps cloud.CloudDependencies) error {
	execution, err := buildrun.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, target)
	if err != nil {
		return err
	}
	buildWithRetry := func(buildInput sharedpkg.DockerBuildSpec, stdout, stderr io.Writer) error {
		return runDockerBuildWithRetry(
			ctx,
			buildInput,
			func(buildInput sharedpkg.DockerBuildSpec, stdout, stderr io.Writer) error {
				if buildDockerImage == nil {
					return buildpkg.DockerImageBuilder(buildInput, stdout, stderr)
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
		if err := execute.RunBuildExecution(ctx, execution, runBuildScript, buildWithRetry, push, cloudStore, cloudDeps); err != nil {
			return err
		}
		return ctx.WriteResult(buildrun.NewBuildResult(execution))
	}
	if err := runBuildAndDeploy(ctx, store, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target, execution, runBuildScript, buildWithRetry, push, deployHelmChart, cloudStore, cloudDeps); err != nil {
		return err
	}
	return ctx.WriteResult(buildrun.NewBuildResult(execution))
}

func runBuildAndDeploy(ctx core.Context, store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, resolveDeployContext deploy.DeployContextResolverFunc, now sharedpkg.NowFunc, target sharedpkg.DockerCommandTarget, execution sharedpkg.BuildExecutionSpec, runBuildScript buildpkg.BuildScriptRunnerFunc, buildWithRetry buildpkg.DockerImageBuilderFunc, push buildpkg.DockerPushFunc, deployHelmChart deploy.HelmChartDeployerFunc, cloudStore config.CloudReadStore, cloudDeps cloud.CloudDependencies) error {
	if buildrun.BuildExecutionUsesBuildScript(execution) {
		return errors.New("build deploy is not supported for project build scripts")
	}

	buildDeployStore, ok := any(store).(deploy.BuildDeployStore)
	if !ok {
		return errors.New("store does not support deploy resolution")
	}

	deploySpecs, err := deployrun.ResolveCurrentDeploySpecsForDockerTarget(ctx, buildDeployStore, findProjectRoot, resolveBuildContext, resolveDeployContext, now, target)
	if err != nil {
		return err
	}

	return execute.RunBuildExecutionAndDeploy(ctx, execution, deploySpecs, runBuildScript, buildWithRetry, push, deployHelmChart, cloudStore, cloudDeps)
}

func NewPushCmd(store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, now sharedpkg.NowFunc, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, push buildpkg.DockerPushFunc) *cobra.Command {
	target := sharedpkg.DockerCommandTarget{}
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
			ctx := shared.CommandContext(cmd)
			pushInput, buildInput, err := buildrun.ResolveDockerPushSpec(ctx, store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
			return pushpkg.RunPushCommand(ctx, func(ctx core.Context) error {
				return buildrun.RunDockerPushSpec(ctx, pushInput, buildInput, buildWithRetry, push)
			})
		},
	}
	shared.AddDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to publish, produced by erun build")
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache")
	return cmd
}

func pushBuildWithRetry(ctx core.Context, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner) buildpkg.DockerImageBuilderFunc {
	return func(buildInput sharedpkg.DockerBuildSpec, stdout, stderr io.Writer) error {
		return runDockerBuildWithRetry(
			ctx,
			buildInput,
			func(buildInput sharedpkg.DockerBuildSpec, stdout, stderr io.Writer) error {
				if buildDockerImage == nil {
					return buildpkg.DockerImageBuilder(buildInput, stdout, stderr)
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

// NewRootPushCmd keeps --build orchestration here so push stays a pure primitive.
func NewRootPushCmd(store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, now sharedpkg.NowFunc, runBuildScript buildpkg.BuildScriptRunnerFunc, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, push buildpkg.DockerPushFunc, cloudStore config.CloudReadStore, cloudDeps cloud.CloudDependencies) *cobra.Command {
	target := sharedpkg.DockerCommandTarget{}
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
			"version out of `erun build` by hand. It is mutually exclusive with --version.\n\n" +
			"--component selects one of a monorepo's declared .erun/config.yaml components " +
			"entries, the same selector `erun build` takes; see `erun build --help`.",
		Example:       "  erun push --version 1.2.3-snapshot-20260101010101\n  erun push --build\n  erun push --version 1.2.3 --component ingest-worker",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if force {
				target.NoIncremental = true
			}
			ctx := shared.CommandContext(cmd)
			if target.Build {
				if strings.TrimSpace(target.VersionOverride) != "" {
					return errPushBuildVersionConflict
				}
				if err := runPushBuildStep(ctx, store, findProjectRoot, resolveBuildContext, now, &target, runBuildScript, buildDockerImage, loginToDockerRegistry, selectRunner, push, cloudStore, cloudDeps); err != nil {
					return err
				}
			} else if strings.TrimSpace(target.VersionOverride) == "" {
				return errPushVersionRequired
			}
			buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
			buildContext, _ := resolveBuildContext()
			if strings.TrimSpace(buildContext.DockerfilePath) != "" {
				pushInput, buildInput, err := buildrun.ResolveDockerPushSpec(ctx, store, findProjectRoot, resolveBuildContext, now, target)
				if err != nil {
					return err
				}
				return pushpkg.RunPushCommand(ctx, func(ctx core.Context) error {
					return buildrun.RunDockerPushSpec(ctx, pushInput, buildInput, buildWithRetry, push)
				})
			}
			execution, err := buildrun.ResolveDockerPushExecution(ctx, store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			return pushpkg.RunPushCommand(ctx, func(ctx core.Context) error {
				return buildrun.RunDockerPushExecution(ctx, execution, buildWithRetry, push)
			})
		},
	}
	shared.AddDryRunFlag(cmd)
	addPushCommandTargetFlags(cmd, &target)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Version to publish, produced by erun build")
	cmd.Flags().BoolVar(&force, "force", false, "Rebuild and re-push every image, bypassing the fingerprint cache (also forces the --build step)")
	cmd.Flags().BoolVar(&target.Build, "build", false, "Build the current source first, then push the version it mints (operator shortcut for build → push)")
	return cmd
}

func runPushBuildStep(ctx core.Context, store sharedpkg.DockerStore, findProjectRoot sharedpkg.ProjectFinderFunc, resolveBuildContext sharedpkg.BuildContextResolverFunc, now sharedpkg.NowFunc, target *sharedpkg.DockerCommandTarget, runBuildScript buildpkg.BuildScriptRunnerFunc, buildDockerImage buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, push buildpkg.DockerPushFunc, cloudStore config.CloudReadStore, cloudDeps cloud.CloudDependencies) error {
	execution, err := buildrun.ResolveBuildExecution(ctx, store, findProjectRoot, resolveBuildContext, now, *target)
	if err != nil {
		return err
	}
	buildWithRetry := pushBuildWithRetry(ctx, buildDockerImage, loginToDockerRegistry, selectRunner)
	if err := execute.RunBuildExecution(ctx, execution, runBuildScript, buildWithRetry, push, cloudStore, cloudDeps); err != nil {
		return err
	}
	version := strings.TrimSpace(buildrun.NewBuildResult(execution).Version)
	if version == "" {
		return errors.New("erun push --build: the build did not mint a version to push")
	}
	target.VersionOverride = version
	return nil
}

func RunDockerPushWithRetry(ctx core.Context, pushInput sharedpkg.DockerPushSpec, push buildpkg.DockerPushFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner) error {
	err := push(ctx, pushInput)
	if err == nil {
		return nil
	}

	var authErr sharedpkg.DockerRegistryAuthError
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

func runDockerBuildWithRetry(ctx core.Context, buildInput sharedpkg.DockerBuildSpec, build buildpkg.DockerImageBuilderFunc, loginToDockerRegistry buildpkg.DockerRegistryLoginFunc, selectRunner shared.SelectRunner, stdout, stderr io.Writer) error {
	err := build(buildInput, stdout, stderr)
	if err == nil {
		return nil
	}

	var authErr sharedpkg.DockerRegistryAuthError
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

func addBuildCommandTargetFlags(cmd *cobra.Command, target *sharedpkg.DockerCommandTarget) {
	addPushCommandTargetFlags(cmd, target)
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Override the version build would mint")
	cmd.Flags().BoolVar(&target.Deploy, "deploy", false, "Deploy the built version after the build completes")
	cmd.Flags().BoolVar(&target.Release, "release", false, "Run release first and publish the release-tagged images and the linux packages' .deb files")
	cmd.Flags().BoolVar(&target.Force, "force", false, "Delete and recreate conflicting release tags when combined with --release")
	cmd.Flags().BoolVar(&target.NoIncremental, "no-incremental", false, "Disable fingerprint-based build caching and rebuild every image from scratch")
	cmd.Flags().StringSliceVar(&target.Platforms, "platform", nil, "Build only these docker platforms (e.g. linux/amd64), repeatable; overrides the project's configured docker.platforms (project-wide or per-environment). Mutually exclusive with --release, which always publishes every platform erun supports")
}

func addPushCommandTargetFlags(cmd *cobra.Command, target *sharedpkg.DockerCommandTarget) {
	cmd.Flags().StringVar(&target.Component, "component", "", "Select a monorepo component declared under .erun/config.yaml components.<name> (auto-selects the lone entry if only one is declared; unused when the project declares none)")
	cmd.Flags().StringVar(&target.ProjectRoot, "project-root", "", "Project root override for internal tooling")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Environment override for internal tooling")
	_ = cmd.Flags().MarkHidden("project-root")
	_ = cmd.Flags().MarkHidden("environment")
}

// handleNamespaceAuthError re-authenticates as the namespace owner, escalating to
// `gh auth refresh`, which cannot complete headless (device code).
func handleNamespaceAuthError(authErr sharedpkg.DockerRegistryAuthError, retry func() error, stdin io.Reader, stdout, stderr io.Writer) (bool, error) {
	if !sharedpkg.IsDockerCreatePackageDenied(authErr.Message) && !buildpkg.IsDockerScopeDenied(authErr.Message) {
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
	if sharedpkg.IsDockerCreatePackageDenied(authErr.Message) {
		printCreatePackageGuidance(stderr, authErr.Tag, authErr.Registry)
	}
	return true, finalErr
}

func retryAfterNamespaceLogin(authErr sharedpkg.DockerRegistryAuthError, retry func() error, stdout, stderr io.Writer) error {
	ok, _ := sharedpkg.TryGHCRNamespaceLogin(authErr.Tag, stdout, stderr)
	if !ok {
		return authErr
	}
	if retryErr := retry(); retryErr != nil {
		return retryErr
	}
	return nil
}

func retryAfterScopeRefresh(authErr sharedpkg.DockerRegistryAuthError, retry func() error, stdin io.Reader, stdout, stderr io.Writer) error {
	ok, refreshErr := buildpkg.RefreshGHCRPackageScopes(authErr.Tag, stdin, stdout, stderr)
	if refreshErr != nil {
		return refreshErr
	}
	if !ok {
		return authErr
	}
	return retry()
}

func scopeStillDenied(err error) bool {
	var authErr sharedpkg.DockerRegistryAuthError
	if !errors.As(err, &authErr) {
		return false
	}
	return buildpkg.IsDockerScopeDenied(authErr.Message)
}

func printCreatePackageGuidance(out io.Writer, tag, registry string) {
	if out == nil {
		return
	}
	namespace := registrypkg.DockerNamespaceFromTag(tag)
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

func promptDockerLoginRetry(run shared.SelectRunner, registry string) (bool, error) {
	if shared.IsTrueishEnv("ERUN_AUTO_LOGIN_ON_PUSH") {
		return true, nil
	}
	label := fmt.Sprintf("Docker push requires login to %s", buildpkg.DockerRegistryDisplayName(registry))
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
