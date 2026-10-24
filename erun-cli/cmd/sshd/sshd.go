package sshd

import (
	"context"
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy"
	"github.com/sophium/erun/erun-common/primitive/open"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
	"github.com/spf13/cobra"
)

func NewSSHDCmd(prepareContext func(core.Context) core.Context, resolveOpen func(sharedpkg.OpenParams) (config.OpenResult, error), saveEnvConfig func(string, config.EnvConfig) error, runInitForOpen func(core.Context, sharedpkg.OpenParams) error, findProjectRoot sharedpkg.ProjectFinderFunc, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (deploy.DeploySpec, error), deployHelmChart deploy.HelmChartDeployerFunc, runRemoteCommand kube.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) *cobra.Command {
	var publicKeyPath string
	var localPort int
	target := sharedpkg.OpenParams{}

	initCmd := &cobra.Command{
		Use:          "init [TENANT] [ENVIRONMENT]",
		Short:        "Enable SSH access for a remote environment",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			params, err := shared.ResolveOpenParams(cmd.CommandPath(), args, target)
			if err != nil {
				return err
			}
			result, _, err := shared.ResolveOpenWithInitRetryForParams(ctx, params, shared.ShouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			return runSSHDInitCommand(ctx, result, publicKeyPath, localPort, saveEnvConfig, findProjectRoot, resolveRuntimeDeploySpec, deployHelmChart, runRemoteCommand, writeLocalConfig)
		},
	}
	shared.AddDryRunFlag(initCmd)
	initCmd.Flags().StringVar(&target.Tenant, "tenant", "", "Enable SSH for a specific tenant")
	initCmd.Flags().StringVar(&target.Environment, "environment", "", "Enable SSH for a specific environment")
	initCmd.Flags().StringVar(&publicKeyPath, "public-key", "", "Public key to authorize for remote SSH access")
	initCmd.Flags().IntVar(&localPort, "local-port", 0, "Fixed local port to use for kubectl port-forward")

	syncTarget := sharedpkg.OpenParams{}
	syncCmd := &cobra.Command{
		Use:          "sync [TENANT] [ENVIRONMENT]",
		Short:        "Mirror a remote environment's workspace onto this host",
		Args:         cobra.MaximumNArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := shared.CommandContext(cmd)
			if prepareContext != nil {
				ctx = prepareContext(ctx)
			}
			params, err := shared.ResolveOpenParams(cmd.CommandPath(), args, syncTarget)
			if err != nil {
				return err
			}
			result, _, err := shared.ResolveOpenWithInitRetryForParams(ctx, params, shared.ShouldRunInitForOpenCommand, resolveOpen, runInitForOpen)
			if err != nil {
				return err
			}
			return runSSHDSyncCommand(cmd.Context(), ctx, result, findProjectRoot)
		},
	}
	shared.AddDryRunFlag(syncCmd)
	syncCmd.Flags().StringVar(&syncTarget.Tenant, "tenant", "", "Sync a specific tenant")
	syncCmd.Flags().StringVar(&syncTarget.Environment, "environment", "", "Sync a specific environment")

	return shared.NewCommandGroup("sshd", "Remote SSH utilities", initCmd, syncCmd)
}

// runSSHDSyncCommand runs one workspace-sync pass, so an orchestrator whose
// mirror is empty or stale can fill it without the desktop. Each precondition it
// refuses on is named, because "the mirror did not change" is the one symptom
// they all share.
func runSSHDSyncCommand(ctx context.Context, cmdCtx core.Context, result config.OpenResult, findProjectRoot sharedpkg.ProjectFinderFunc) error {
	params, err := workspace.ResolveWorkspaceSyncParams(result, findProjectRoot)
	if err != nil {
		return fmt.Errorf("workspace sync for %s/%s: %w", result.Tenant, result.Environment, err)
	}
	cmdCtx.Trace(fmt.Sprintf("resolve workspace sync %s:%s -> %s", params.HostAlias, params.RemotePath, params.LocalPath))
	if err := workspace.WorkspaceSyncSSHReady(ctx, params.HostAlias); err != nil {
		return fmt.Errorf("workspace sync for %s/%s: the SSH channel to the pod is not up (%s): %w", result.Tenant, result.Environment, params.HostAlias, err)
	}
	pass := workspace.SyncWorkspaceOnce
	if cmdCtx.DryRun {
		cmdCtx.Trace("dry run: reporting what one pass would change, without touching the mirror")
		pass = workspace.PreviewWorkspaceSync
	}
	synced, err := pass(ctx, params)
	if err != nil {
		return err
	}
	cmdCtx.Trace(shared.SshdSyncSummary(cmdCtx.DryRun, synced))
	return cmdCtx.WriteResult(synced)
}

func runSSHDInitCommand(ctx core.Context, result config.OpenResult, publicKeyPath string, localPort int, saveEnvConfig func(string, config.EnvConfig) error, findProjectRoot sharedpkg.ProjectFinderFunc, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (deploy.DeploySpec, error), deployHelmChart deploy.HelmChartDeployerFunc, runRemoteCommand kube.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) error {
	// The desktop pipes `erun sshd init` into its shared Local shell, which
	// never produces a PTY exit (see erun-ui/AGENTS.md § "Command Completion
	// And State-Refresh Wiring"), so these are the only completion signal the
	// desktop has: an activity-queue entry to show the run in progress, and a
	// recorded outcome the Manage dialog can display as the last run's result.
	ctx.Info(fmt.Sprintf("==> SSHD init %s/%s", result.Tenant, result.Environment))
	if err := runSSHDInitForTarget(ctx, result, publicKeyPath, localPort, saveEnvConfig, findProjectRoot, resolveRuntimeDeploySpec, deployHelmChart, runRemoteCommand, writeLocalConfig); err != nil {
		ctx.Info(fmt.Sprintf("==> SSHD init failed %s/%s: %s", result.Tenant, result.Environment, err.Error()))
		return err
	}
	ctx.Info(fmt.Sprintf("==> SSHD init done %s/%s", result.Tenant, result.Environment))
	return nil
}

func runSSHDInitForTarget(ctx core.Context, result config.OpenResult, publicKeyPath string, localPort int, saveEnvConfig func(string, config.EnvConfig) error, findProjectRoot sharedpkg.ProjectFinderFunc, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (deploy.DeploySpec, error), deployHelmChart deploy.HelmChartDeployerFunc, runRemoteCommand kube.RemoteCommandRunnerFunc, writeLocalConfig SSHDLocalConfigWriter) error {
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
	if _, err := shared.SyncRemoteSSHDKey(ctx, result, runRemoteCommand); err != nil {
		return err
	}
	localConfig, err := writeSSHDLocalConfig(ctx, result, writeLocalConfig)
	if err != nil {
		return err
	}
	return writeSSHDInitSummary(ctx, result, localConfig)
}

func validateSSHDInitDependencies(result config.OpenResult, saveEnvConfig func(string, config.EnvConfig) error, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (deploy.DeploySpec, error), deployHelmChart deploy.HelmChartDeployerFunc) error {
	if err := open.ValidateSSHDTarget(result); err != nil {
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

func resolveSSHDEnvConfig(result config.OpenResult, publicKeyPath string, localPort int, findProjectRoot sharedpkg.ProjectFinderFunc) (config.EnvConfig, error) {
	if publicKeyPath == "" {
		publicKeyPath = result.EnvConfig.SSHD.PublicKeyPath
	}
	resolvedPublicKeyPath, _, err := shared.ResolveSSHDPublicKey(publicKeyPath)
	if err != nil {
		return config.EnvConfig{}, err
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
		updatedEnv.SSHD.LocalPort = config.SSHLocalPortForResult(result)
	}
	return updatedEnv, nil
}

func resolveSSHDWorkspaceSyncLocalPath(result config.OpenResult, findProjectRoot sharedpkg.ProjectFinderFunc) string {
	if findProjectRoot != nil {
		if _, projectRoot, err := findProjectRoot(); err == nil && strings.TrimSpace(projectRoot) != "" {
			return strings.TrimSpace(projectRoot)
		}
	}
	return strings.TrimSpace(result.EnvConfig.SSHD.WorkspaceSync.LocalPath)
}

func saveSSHDEnvConfig(ctx core.Context, result config.OpenResult, updatedEnv config.EnvConfig, saveEnvConfig func(string, config.EnvConfig) error) error {
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("save SSHD config for %s/%s", result.Tenant, result.Environment))
		if updatedEnv.SSHD.WorkspaceSync.Enabled {
			ctx.Trace(fmt.Sprintf("enable SSHD workspace sync for %s/%s to %s", result.Tenant, result.Environment, shared.ValueOrNone(updatedEnv.SSHD.WorkspaceSync.LocalPath)))
		}
		return nil
	}
	return saveEnvConfig(result.Tenant, updatedEnv)
}

func deploySSHDConfig(ctx core.Context, result config.OpenResult, resolveRuntimeDeploySpec func(core.Context, config.OpenResult, bool) (deploy.DeploySpec, error), deployHelmChart deploy.HelmChartDeployerFunc) error {
	spec, err := resolveRuntimeDeploySpec(ctx, result, false)
	if err != nil {
		return err
	}
	return deploy.RunDeploySpec(ctx, spec, deployHelmChart)
}

func writeSSHDLocalConfig(ctx core.Context, result config.OpenResult, writeLocalConfig SSHDLocalConfigWriter) (shared.SSHDLocalConfigResult, error) {
	if writeLocalConfig == nil {
		return shared.SSHDLocalConfigResult{}, nil
	}
	if ctx.DryRun {
		info := open.SSHConnectionInfoForResult(result)
		ctx.Trace(fmt.Sprintf("write ssh config host %s for %s/%s", info.HostAlias, result.Tenant, result.Environment))
		return shared.SSHDLocalConfigResult{}, nil
	}
	return writeLocalConfig(result)
}

func writeSSHDInitSummary(ctx core.Context, result config.OpenResult, localConfig shared.SSHDLocalConfigResult) error {
	if ctx.Stdout != nil {
		info := open.SSHConnectionInfoForResult(result)
		if _, err := fmt.Fprintf(
			ctx.Stdout,
			"SSHD enabled for %s/%s\n  host: %s\n  config: %s\n  user: %s\n  local port: %d\n  workspace: %s\n",
			result.Tenant,
			result.Environment,
			shared.ValueOrNone(localConfig.HostAlias),
			shared.ValueOrNone(localConfig.ConfigPath),
			info.User,
			info.Port,
			info.WorkspacePath,
		); err != nil {
			return err
		}
	}
	return nil
}
