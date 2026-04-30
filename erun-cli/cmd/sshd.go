package cmd

import (
	"fmt"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newSSHDCmd(prepareContext func(common.Context) common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, runInitForOpen func(common.Context, common.OpenParams) error, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) *cobra.Command {
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
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			params, err := resolveOpenParams(args, target)
			if err != nil {
				return err
			}
			result, _, err := resolveOpenWithInitRetryForParams(ctx, params, shouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			return runSSHDInitCommand(ctx, result, publicKeyPath, localPort, saveEnvConfig, resolveRuntimeDeploySpec, deployHelmChart, runRemoteCommand, writeLocalConfig)
		},
	}
	addDryRunFlag(initCmd)
	initCmd.Flags().StringVar(&target.Tenant, "tenant", "", "Enable SSH for a specific tenant")
	initCmd.Flags().StringVar(&target.Environment, "environment", "", "Enable SSH for a specific environment")
	initCmd.Flags().StringVar(&publicKeyPath, "public-key", "", "Public key to authorize for remote SSH access")
	initCmd.Flags().IntVar(&localPort, "local-port", 0, "Fixed local port to use for kubectl port-forward")

	return newCommandGroup("sshd", "Remote SSH utilities", initCmd)
}

func runSSHDInitCommand(ctx common.Context, result common.OpenResult, publicKeyPath string, localPort int, saveEnvConfig func(string, common.EnvConfig) error, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) error {
	if err := validateSSHDInitDependencies(result, saveEnvConfig, resolveRuntimeDeploySpec, deployHelmChart); err != nil {
		return err
	}
	updatedEnv, err := resolveSSHDEnvConfig(result, publicKeyPath, localPort)
	if err != nil {
		return err
	}
	if err := saveSSHDEnvConfig(ctx, result, updatedEnv, saveEnvConfig); err != nil {
		return err
	}

	result.EnvConfig = updatedEnv
	if err := deploySSHDConfig(ctx, result, resolveRuntimeDeploySpec, deployHelmChart); err != nil {
		return err
	}
	if _, err := syncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
		return err
	}
	localConfig, err := writeSSHDLocalConfig(ctx, result, writeLocalConfig)
	if err != nil {
		return err
	}
	return writeSSHDInitSummary(ctx, result, localConfig)
}

func validateSSHDInitDependencies(result common.OpenResult, saveEnvConfig func(string, common.EnvConfig) error, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
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
	return nil
}

func resolveSSHDEnvConfig(result common.OpenResult, publicKeyPath string, localPort int) (common.EnvConfig, error) {
	if publicKeyPath == "" {
		publicKeyPath = result.EnvConfig.SSHD.PublicKeyPath
	}
	resolvedPublicKeyPath, _, err := resolveSSHDPublicKey(publicKeyPath)
	if err != nil {
		return common.EnvConfig{}, err
	}
	updatedEnv := result.EnvConfig
	updatedEnv.SSHD.Enabled = true
	updatedEnv.SSHD.PublicKeyPath = resolvedPublicKeyPath
	if localPort > 0 {
		updatedEnv.SSHD.LocalPort = localPort
	}
	if updatedEnv.SSHD.LocalPort == 0 {
		updatedEnv.SSHD.LocalPort = common.SSHLocalPortForResult(result)
	}
	return updatedEnv, nil
}

func saveSSHDEnvConfig(ctx common.Context, result common.OpenResult, updatedEnv common.EnvConfig, saveEnvConfig func(string, common.EnvConfig) error) error {
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("save SSHD config for %s/%s", result.Tenant, result.Environment))
		return nil
	}
	return saveEnvConfig(result.Tenant, updatedEnv)
}

func deploySSHDConfig(ctx common.Context, result common.OpenResult, resolveRuntimeDeploySpec func(common.OpenResult) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
	spec, err := resolveRuntimeDeploySpec(result)
	if err != nil {
		return err
	}
	return common.RunDeploySpec(ctx, spec, nil, nil, deployHelmChart)
}

func writeSSHDLocalConfig(ctx common.Context, result common.OpenResult, writeLocalConfig SSHDLocalConfigWriter) (SSHDLocalConfigResult, error) {
	if writeLocalConfig == nil {
		return SSHDLocalConfigResult{}, nil
	}
	if ctx.DryRun {
		info := common.SSHConnectionInfoForResult(result)
		ctx.Trace(fmt.Sprintf("write ssh config host %s for %s/%s", info.HostAlias, result.Tenant, result.Environment))
		return SSHDLocalConfigResult{}, nil
	}
	return writeLocalConfig(result)
}

func writeSSHDInitSummary(ctx common.Context, result common.OpenResult, localConfig SSHDLocalConfigResult) error {
	if ctx.Stdout != nil {
		info := common.SSHConnectionInfoForResult(result)
		if _, err := fmt.Fprintf(
			ctx.Stdout,
			"SSHD enabled for %s/%s\n  host: %s\n  config: %s\n  user: %s\n  local port: %d\n  workspace: %s\n",
			result.Tenant,
			result.Environment,
			valueOrNone(localConfig.HostAlias),
			valueOrNone(localConfig.ConfigPath),
			info.User,
			info.Port,
			info.WorkspacePath,
		); err != nil {
			return err
		}
	}
	return nil
}
