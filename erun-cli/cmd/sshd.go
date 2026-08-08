package cmd

import (
	"context"
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func newSSHDCmd(prepareContext func(common.Context) common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), saveEnvConfig func(string, common.EnvConfig) error, runInitForOpen func(common.Context, common.OpenParams) error, findProjectRoot common.ProjectFinderFunc, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) *cobra.Command {
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
			return runSSHDInitCommand(ctx, result, publicKeyPath, localPort, saveEnvConfig, findProjectRoot, resolveRuntimeDeploySpec, deployHelmChart, runRemoteCommand, writeLocalConfig)
		},
	}
	addDryRunFlag(initCmd)
	initCmd.Flags().StringVar(&target.Tenant, "tenant", "", "Enable SSH for a specific tenant")
	initCmd.Flags().StringVar(&target.Environment, "environment", "", "Enable SSH for a specific environment")
	initCmd.Flags().StringVar(&publicKeyPath, "public-key", "", "Public key to authorize for remote SSH access")
	initCmd.Flags().IntVar(&localPort, "local-port", 0, "Fixed local port to use for kubectl port-forward")

	syncTarget := common.OpenParams{}
	syncCmd := &cobra.Command{
		Use:          "sync [TENANT] [ENVIRONMENT]",
		Short:        "Mirror a remote environment's workspace onto this host",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := commandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			params, err := resolveOpenParams(args, syncTarget)
			if err != nil {
				return err
			}
			result, _, err := resolveOpenWithInitRetryForParams(ctx, params, shouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			return runSSHDSyncCommand(cmd.Context(), ctx, result, findProjectRoot)
		},
	}
	addDryRunFlag(syncCmd)
	syncCmd.Flags().StringVar(&syncTarget.Tenant, "tenant", "", "Sync a specific tenant")
	syncCmd.Flags().StringVar(&syncTarget.Environment, "environment", "", "Sync a specific environment")

	return newCommandGroup("sshd", "Remote SSH utilities", initCmd, syncCmd)
}

// runSSHDSyncCommand runs one workspace-sync pass, so an orchestrator whose
// mirror is empty or stale can fill it without the desktop. Each precondition it
// refuses on is named, because "the mirror did not change" is the one symptom
// they all share.
func runSSHDSyncCommand(ctx context.Context, cmdCtx common.Context, result common.OpenResult, findProjectRoot common.ProjectFinderFunc) error {
	params, err := common.ResolveWorkspaceSyncParams(result, findProjectRoot)
	if err != nil {
		return fmt.Errorf("workspace sync for %s/%s: %w", result.Tenant, result.Environment, err)
	}
	cmdCtx.Trace(fmt.Sprintf("resolve workspace sync %s:%s -> %s", params.HostAlias, params.RemotePath, params.LocalPath))
	if err := common.WorkspaceSyncSSHReady(ctx, params.HostAlias); err != nil {
		return fmt.Errorf("workspace sync for %s/%s: the SSH channel to the pod is not up (%s): %w", result.Tenant, result.Environment, params.HostAlias, err)
	}
	pass := common.SyncWorkspaceOnce
	if cmdCtx.DryRun {
		cmdCtx.Trace("dry run: reporting what one pass would change, without touching the mirror")
		pass = common.PreviewWorkspaceSync
	}
	synced, err := pass(ctx, params)
	if err != nil {
		return err
	}
	cmdCtx.Trace(sshdSyncSummary(cmdCtx.DryRun, synced))
	return cmdCtx.WriteResult(synced)
}

func sshdSyncSummary(dryRun bool, synced common.WorkspaceSyncResult) string {
	if dryRun {
		return fmt.Sprintf("workspace sync would copy %d files, delete %d, and deliver %d artifacts", synced.FilesCopied, synced.FilesDeleted, synced.ArtifactsCopied)
	}
	return fmt.Sprintf("workspace sync copied %d files, deleted %d, delivered %d artifacts", synced.FilesCopied, synced.FilesDeleted, synced.ArtifactsCopied)
}

func runSSHDInitCommand(ctx common.Context, result common.OpenResult, publicKeyPath string, localPort int, saveEnvConfig func(string, common.EnvConfig) error, findProjectRoot common.ProjectFinderFunc, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc, runRemoteCommand common.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) error {
	if err := validateSSHDInitDependencies(result, saveEnvConfig, resolveRuntimeDeploySpec, deployHelmChart); err != nil {
		return err
	}
	updatedEnv, err := resolveSSHDEnvConfig(result, publicKeyPath, localPort, findProjectRoot)
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

func validateSSHDInitDependencies(result common.OpenResult, saveEnvConfig func(string, common.EnvConfig) error, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
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

func resolveSSHDEnvConfig(result common.OpenResult, publicKeyPath string, localPort int, findProjectRoot common.ProjectFinderFunc) (common.EnvConfig, error) {
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
	if updatedEnv.SSHD.WorkspaceSync.Enabled {
		if localPath := resolveSSHDWorkspaceSyncLocalPath(result, findProjectRoot); localPath != "" {
			updatedEnv.SSHD.WorkspaceSync.LocalPath = localPath
		}
	}
	if localPort > 0 {
		updatedEnv.SSHD.LocalPort = localPort
	}
	if updatedEnv.SSHD.LocalPort == 0 {
		updatedEnv.SSHD.LocalPort = common.SSHLocalPortForResult(result)
	}
	return updatedEnv, nil
}

func resolveSSHDWorkspaceSyncLocalPath(result common.OpenResult, findProjectRoot common.ProjectFinderFunc) string {
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return strings.TrimSpace(projectRoot)
		}
	}
	return strings.TrimSpace(result.EnvConfig.SSHD.WorkspaceSync.LocalPath)
}

func saveSSHDEnvConfig(ctx common.Context, result common.OpenResult, updatedEnv common.EnvConfig, saveEnvConfig func(string, common.EnvConfig) error) error {
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("save SSHD config for %s/%s", result.Tenant, result.Environment))
		if updatedEnv.SSHD.WorkspaceSync.Enabled {
			ctx.Trace(fmt.Sprintf("enable SSHD workspace sync for %s/%s to %s", result.Tenant, result.Environment, valueOrNone(updatedEnv.SSHD.WorkspaceSync.LocalPath)))
		}
		return nil
	}
	return saveEnvConfig(result.Tenant, updatedEnv)
}

func deploySSHDConfig(ctx common.Context, result common.OpenResult, resolveRuntimeDeploySpec func(common.Context, common.OpenResult, bool) (common.DeploySpec, error), deployHelmChart common.HelmChartDeployerFunc) error {
	spec, err := resolveRuntimeDeploySpec(ctx, result, false)
	if err != nil {
		return err
	}
	return common.RunDeploySpec(ctx, spec, deployHelmChart)
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
