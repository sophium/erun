package doctor

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// runWorkspaceSyncDoctor diagnoses and optionally repairs a remote-agent env's host-side
// SSH provisioning, without the helm redeploy `erun sshd init` would run.
func runWorkspaceSyncDoctor(ctx core.Context, promptRunner shared.PromptRunner, configStore config.ConfigStore, result config.OpenResult, options doctorOptions) error {
	if !doctorWorkspaceSyncApplicable(result) {
		if options.repairWorkspaceSync {
			_, err := fmt.Fprintf(ctx.Stdout, "Workspace sync is not enabled for %s/%s; nothing to repair.\n", result.Tenant, result.Environment)
			return err
		}
		return nil
	}
	healthy, err := diagnoseAndReportWorkspaceSync(ctx, result)
	if err != nil {
		return err
	}
	repair, err := shouldRepairWorkspaceSync(ctx, promptRunner, result, options, healthy)
	if err != nil || !repair {
		return err
	}
	return repairWorkspaceSyncProvisioning(ctx, configStore, result)
}

func doctorWorkspaceSyncApplicable(result config.OpenResult) bool {
	return result.RemoteRepo() && result.EnvConfig.SSHD.Enabled && result.EnvConfig.SSHD.WorkspaceSync.Enabled
}

func doctorOnlyRepairWorkspaceSync(options doctorOptions) bool {
	return options.repairWorkspaceSync &&
		strings.TrimSpace(options.restoreConfigFromBackup) == "" &&
		strings.TrimSpace(options.restoreEnvConfigFromBackup) == "" &&
		!anyDoctorActionRequested(
			options.pruneImages,
			options.pruneBuildCache,
			options.pruneContainers,
			options.repairJetBrainsGateway,
			options.clearPendingHelm,
			options.rollback,
			options.finishRemoteInit,
			options.repairConfig,
		)
}

func anyDoctorActionRequested(flags ...bool) bool {
	for _, f := range flags {
		if f {
			return true
		}
	}
	return false
}

// diagnoseAndReportWorkspaceSync returns whether SSH reaches the pod; dry-run only traces.
func diagnoseAndReportWorkspaceSync(ctx core.Context, result config.OpenResult) (bool, error) {
	info := open.SSHConnectionInfoForResult(result)
	mirror := shared.ValueOrNone(strings.TrimSpace(result.EnvConfig.SSHD.WorkspaceSync.LocalPath))
	if _, err := fmt.Fprintf(ctx.Stdout, "== Workspace sync (host mirror) ==\n  host alias: %s\n  mirror: %s\n", info.HostAlias, mirror); err != nil {
		return false, err
	}
	if ctx.DryRun {
		ctx.Trace("workspace-sync: would resolve the SSH public key and the local ssh config alias")
		ctx.TraceCommand("", "ssh", workspaceSyncReachArgs(info.HostAlias)...)
		return false, nil
	}
	if keyPath, _, err := shared.ResolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath); err != nil {
		if _, e := fmt.Fprintf(ctx.Stdout, "  ssh key: not found (%s)\n", err.Error()); e != nil {
			return false, e
		}
	} else if _, e := fmt.Fprintf(ctx.Stdout, "  ssh key: %s\n", keyPath); e != nil {
		return false, e
	}
	reachable, reason := probeWorkspaceSyncSSH(info.HostAlias)
	if reachable {
		_, err := fmt.Fprintln(ctx.Stdout, "  ssh reachable: yes")
		return true, err
	}
	_, err := fmt.Fprintf(ctx.Stdout, "  ssh reachable: no (%s)\n", reason)
	return false, err
}

// shouldRepairWorkspaceSync: the flag wins; otherwise offer only interactively on a problem.
func shouldRepairWorkspaceSync(ctx core.Context, promptRunner shared.PromptRunner, result config.OpenResult, options doctorOptions, healthy bool) (bool, error) {
	if options.repairWorkspaceSync {
		return true, nil
	}
	if healthy || ctx.DryRun || promptRunner == nil {
		return false, nil
	}
	return doctorConfirm(ctx, promptRunner, fmt.Sprintf(
		"Repair workspace sync SSH for %s/%s (install key, write ssh config, ensure port-forward; no redeploy)?",
		result.Tenant, result.Environment),
		"Re-run with --repair-workspace-sync to run it without a prompt.")
}

func repairWorkspaceSyncProvisioning(ctx core.Context, configStore config.ConfigStore, result config.OpenResult) error {
	if err := persistWorkspaceSyncKeyPath(ctx, configStore, &result); err != nil {
		return err
	}
	if err := writeWorkspaceSyncLocalSSHConfig(ctx, result); err != nil {
		return err
	}
	if _, err := shared.SyncRemoteSSHDKey(ctx, result, kube.RunRemoteCommand); err != nil {
		return err
	}
	if _, err := shared.EnsureSSHDPortForward(ctx, result); err != nil {
		// A port-forward that never serves SSH usually means no SSHD has been deployed yet.
		return reportWorkspaceSyncSSHDMissing(ctx, result, err)
	}
	return reportWorkspaceSyncRepairOutcome(ctx, result)
}

func reportWorkspaceSyncSSHDMissing(ctx core.Context, result config.OpenResult, cause error) error {
	_, err := fmt.Fprintf(ctx.Stdout,
		"Installed the SSH key and wrote the ssh config, but the SSH port-forward did not come up:\n  %s\nThe runtime pod most likely has no SSHD running; run `erun sshd init %s %s` to redeploy and enable it (this repair does not redeploy).\n",
		strings.TrimSpace(cause.Error()), result.Tenant, result.Environment)
	return err
}

// persistWorkspaceSyncKeyPath records the resolved key when the env config has none.
func persistWorkspaceSyncKeyPath(ctx core.Context, configStore config.ConfigStore, result *config.OpenResult) error {
	publicKeyPath, _, err := shared.ResolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath)
	if err != nil {
		if !ctx.DryRun {
			return fmt.Errorf("resolve SSH public key: %w", err)
		}
		ctx.Trace("workspace-sync: dry-run: " + err.Error() + "; using placeholder key path for trace")
		publicKeyPath = "<no-public-key>"
	}
	if strings.TrimSpace(result.EnvConfig.SSHD.PublicKeyPath) != "" {
		return nil
	}
	updated := result.EnvConfig
	updated.SSHD.PublicKeyPath = publicKeyPath
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("workspace-sync: save SSH public key path %s for %s/%s", publicKeyPath, result.Tenant, result.Environment))
		result.EnvConfig = updated
		return nil
	}
	if err := configStore.SaveEnvConfig(result.Tenant, updated); err != nil {
		return fmt.Errorf("save SSH public key path: %w", err)
	}
	result.EnvConfig = updated
	return nil
}

func writeWorkspaceSyncLocalSSHConfig(ctx core.Context, result config.OpenResult) error {
	info := open.SSHConnectionInfoForResult(result)
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("workspace-sync: write ssh config host %s for %s/%s", info.HostAlias, result.Tenant, result.Environment))
		return nil
	}
	localConfig, err := shared.WriteLocalSSHConfig(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Wrote ssh config host %s to %s\n", localConfig.HostAlias, localConfig.ConfigPath)
	return err
}

func reportWorkspaceSyncRepairOutcome(ctx core.Context, result config.OpenResult) error {
	info := open.SSHConnectionInfoForResult(result)
	if ctx.DryRun {
		ctx.TraceCommand("", "ssh", workspaceSyncReachArgs(info.HostAlias)...)
		return nil
	}
	if reachable, _ := probeWorkspaceSyncSSH(info.HostAlias); reachable {
		// The repair cannot tell whether a sync pass will run, so name the command that runs one.
		_, err := fmt.Fprintf(ctx.Stdout,
			"Workspace sync SSH is reachable for %s/%s. The desktop mirrors this env while it is running; to fill or refresh the mirror now, run `erun sshd sync %s %s`.\n",
			result.Tenant, result.Environment, result.Tenant, result.Environment)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"SSH still cannot reach %s/%s. The runtime pod likely has no SSHD deployed; run `erun sshd init %s %s` to redeploy and enable it (this repair does not redeploy).\n",
		result.Tenant, result.Environment, result.Tenant, result.Environment)
	return err
}

func probeWorkspaceSyncSSH(alias string) (bool, string) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return false, "no ssh host alias"
	}
	cmd := core.Command("ssh", workspaceSyncReachArgs(alias)...)
	output, err := cmd.CombinedOutput()
	if err == nil {
		return true, ""
	}
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		detail = err.Error()
	}
	return false, detail
}

func workspaceSyncReachArgs(alias string) []string {
	return []string{
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=5",
		"-o", "StrictHostKeyChecking=no",
		"-o", "UserKnownHostsFile=/dev/null",
		"-o", "LogLevel=ERROR",
		strings.TrimSpace(alias),
		"true",
	}
}
