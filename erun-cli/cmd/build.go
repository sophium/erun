package cmd

import (
	"errors"
	"fmt"
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

func newBuildCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, runBuildScript common.BuildScriptRunnerFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	cmd := &cobra.Command{
		Use:           "build",
		Short:         "Build the container image in the current directory",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			execution, err := common.ResolveBuildExecution(store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			return common.RunBuildExecution(ctx, execution, runBuildScript, buildDockerImage, push)
		},
	}
	addDryRunFlag(cmd)
	addDockerCommandTargetFlags(cmd, &target)
	return cmd
}

func newPushCmd(store common.DockerStore, findProjectRoot common.ProjectFinderFunc, resolveBuildContext common.BuildContextResolverFunc, now common.NowFunc, buildDockerImage common.DockerImageBuilderFunc, push common.DockerPushFunc) *cobra.Command {
	target := common.DockerCommandTarget{}
	cmd := &cobra.Command{
		Use:           "push",
		Short:         "Build and push the current container image",
		Args:          cobra.NoArgs,
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			pushInput, buildInput, err := common.ResolveDockerPushSpec(store, findProjectRoot, resolveBuildContext, now, target)
			if err != nil {
				return err
			}
			return common.RunDockerPushSpec(ctx, pushInput, buildInput, buildDockerImage, push)
		},
	}
	addDryRunFlag(cmd)
	addDockerCommandTargetFlags(cmd, &target)
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

func addDockerCommandTargetFlags(cmd *cobra.Command, target *common.DockerCommandTarget) {
	cmd.Flags().StringVar(&target.ProjectRoot, "project-root", "", "Project root override for internal tooling")
	cmd.Flags().StringVar(&target.Environment, "environment", "", "Environment override for internal tooling")
	cmd.Flags().StringVar(&target.VersionOverride, "version", "", "Override the resolved image version")
	_ = cmd.Flags().MarkHidden("project-root")
	_ = cmd.Flags().MarkHidden("environment")
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
