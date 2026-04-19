package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newSSHDCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, runInitForOpen func(common.Context, common.OpenParams) error, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc) *cobra.Command {
	var publicKeyPath string
	var localPort int
	target := common.OpenParams{}

	initCmd := &cobra.Command{
		Use:          "init [TENANT] [ENVIRONMENT]",
		Short:        "Enable SSH access for a remote environment",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			params, err := resolveOpenParams(args, target)
			if err != nil {
				return err
			}
			result, _, err := resolveOpenWithInitRetryForParams(ctx, params, shouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			return runSSHDInitCommand(ctx, result, publicKeyPath, localPort, saveEnvConfig, resolveRuntimeDeploySpec, deployHelmChart, runRemoteCommand)
		},
	}
	addDryRunFlag(initCmd)
	initCmd.Flags().StringVar(&target.Tenant, "tenant", "", "Enable SSH for a specific tenant")
	initCmd.Flags().StringVar(&target.Environment, "environment", "", "Enable SSH for a specific environment")
	initCmd.Flags().StringVar(&publicKeyPath, "public-key", "", "Public key to authorize for remote SSH access")
	initCmd.Flags().IntVar(&localPort, "local-port", 0, "Fixed local port to use for kubectl port-forward")

	return newCommandGroup("sshd", "Remote SSH utilities", initCmd)
}

func runSSHDInitCommand(ctx common.Context, result common.OpenResult, publicKeyPath string, localPort int, saveEnvConfig func(string, common.EnvConfig) error, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc) error {
	if err := common.ValidateSSHDTarget(result); err != nil {
		return err
	}
	if saveEnvConfig == nil {
		return fmt.Errorf("environment config saver is required")
	}
	if resolveRuntimeDeploySpec == nil {
		return fmt.Errorf("runtime deploy spec resolver is required")
	}
	if deployHelmChart == nil {
		return fmt.Errorf("helm deployer is required")
	}

	if publicKeyPath == "" {
		publicKeyPath = result.EnvConfig.SSHD.PublicKeyPath
	}
	resolvedPublicKeyPath, _, err := resolveSSHDPublicKey(publicKeyPath)
	if err != nil {
		return err
	}

	updatedEnv := result.EnvConfig
	updatedEnv.SSHD.Enabled = true
	updatedEnv.SSHD.PublicKeyPath = resolvedPublicKeyPath
	if localPort > 0 {
		updatedEnv.SSHD.LocalPort = localPort
	}
	if updatedEnv.SSHD.LocalPort == 0 {
		updatedEnv.SSHD.LocalPort = common.DefaultSSHLocalPort
	}
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("save SSHD config for %s/%s", result.Tenant, result.Environment))
	} else if err := saveEnvConfig(result.Tenant, updatedEnv); err != nil {
		return err
	}

	result.EnvConfig = updatedEnv
	spec, err := resolveRuntimeDeploySpec(result)
	if err != nil {
		return err
	}
	if err := common.RunDeploySpec(ctx, spec, nil, nil, deployHelmChart); err != nil {
		return err
	}
	if _, err := syncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
		return err
	}

	if ctx.Stdout != nil {
		info := common.SSHConnectionInfoForResult(result)
		if _, err := fmt.Fprintf(
			ctx.Stdout,
			"SSHD enabled for %s/%s\n  user: %s\n  local port: %d\n  workspace: %s\n",
			result.Tenant,
			result.Environment,
			info.User,
			info.Port,
			info.WorkspacePath,
		); err != nil {
			return err
		}
	}
	return nil
}
