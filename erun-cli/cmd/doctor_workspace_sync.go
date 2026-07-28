package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// runWorkspaceSyncDoctor diagnoses, and optionally repairs, the host-side SSH
// provisioning a remote-agent env needs before its worktree can mirror to this
// machine. The repair is the sshd-init provisioning MINUS the helm redeploy:
// resolve/persist the SSH key, write the local ssh config alias, install the
// pod authorized_keys (via kubectl exec, not a redeploy), and ensure the SSH
// port-forward. When SSH still cannot reach the pod afterwards, the pod likely
// has no SSHD deployed, so it points at `erun sshd init` for the redeploy the
// repair deliberately will not run.
func runWorkspaceSyncDoctor(ctx common.Context, promptRunner PromptRunner, configStore common.ConfigStore, result common.OpenResult, options doctorOptions) error {
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

// doctorWorkspaceSyncApplicable reports whether the env is a remote-agent env
// with SSHD and workspace sync enabled — the only shape this repair targets.
func doctorWorkspaceSyncApplicable(result common.OpenResult) bool {
	return result.RemoteRepo() && result.EnvConfig.SSHD.Enabled && result.EnvConfig.SSHD.WorkspaceSync.Enabled
}

// doctorOnlyRepairWorkspaceSync reports that --repair-workspace-sync was the
// only action requested, so doctor stops after it instead of falling through to
// the deploy diagnosis and prune prompts.
func doctorOnlyRepairWorkspaceSync(options doctorOptions) bool {
	return options.repairWorkspaceSync &&
		!options.pruneImages &&
		!options.pruneBuildCache &&
		!options.pruneContainers &&
		!options.repairJetBrainsGateway &&
		!options.clearPendingHelm &&
		!options.rollback &&
		!options.finishRemoteInit &&
		!options.repairConfig &&
		strings.TrimSpace(options.restoreConfigFromBackup) == "" &&
		strings.TrimSpace(options.restoreEnvConfigFromBackup) == ""
}

// diagnoseAndReportWorkspaceSync prints the workspace-sync provisioning state and
// returns whether SSH already reaches the pod. In dry-run it traces the checks
// it would run instead of touching the filesystem or the network, so the
// integration goldens stay deterministic.
func diagnoseAndReportWorkspaceSync(ctx common.Context, result common.OpenResult) (bool, error) {
	info := common.SSHConnectionInfoForResult(result)
	mirror := valueOrNone(strings.TrimSpace(result.EnvConfig.SSHD.WorkspaceSync.LocalPath))
	if _, err := fmt.Fprintf(ctx.Stdout, "== Workspace sync (host mirror) ==\n  host alias: %s\n  mirror: %s\n", info.HostAlias, mirror); err != nil {
		return false, err
	}
	if ctx.DryRun {
		ctx.Trace("workspace-sync: would resolve the SSH public key and the local ssh config alias")
		ctx.TraceCommand("", "ssh", workspaceSyncReachArgs(info.HostAlias)...)
		return false, nil
	}
	if keyPath, _, err := resolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath); err != nil {
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

// shouldRepairWorkspaceSync resolves whether to run the repair: the explicit
// flag always wins; otherwise offer it interactively only when the diagnosis
// found a problem, and never in dry-run or non-interactive contexts.
func shouldRepairWorkspaceSync(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions, healthy bool) (bool, error) {
	if options.repairWorkspaceSync {
		return true, nil
	}
	if healthy || ctx.DryRun || promptRunner == nil {
		return false, nil
	}
	return confirmPrompt(promptRunner, fmt.Sprintf(
		"Repair workspace sync SSH for %s/%s (install key, write ssh config, ensure port-forward; no redeploy)?",
		result.Tenant, result.Environment))
}

// repairWorkspaceSyncProvisioning runs the non-destructive provisioning steps.
func repairWorkspaceSyncProvisioning(ctx common.Context, configStore common.ConfigStore, result common.OpenResult) error {
	if err := persistWorkspaceSyncKeyPath(ctx, configStore, &result); err != nil {
		return err
	}
	if err := writeWorkspaceSyncLocalSSHConfig(ctx, result); err != nil {
		return err
	}
	if _, err := syncRemoteSSHDKey(ctx, result, common.RunRemoteCommand); err != nil {
		return err
	}
	if _, err := ensureSSHDPortForward(ctx, result); err != nil {
		// A port-forward that never serves SSH almost always means the pod has no
		// SSHD running yet (the env config enables it but no deploy carried it).
		// The key and ssh config are already in place, so this is not a repair
		// failure — surface the cause and the redeploy next step instead of a raw
		// error, matching the not-reachable outcome below.
		return reportWorkspaceSyncSSHDMissing(ctx, result, err)
	}
	return reportWorkspaceSyncRepairOutcome(ctx, result)
}

// reportWorkspaceSyncSSHDMissing explains that the local provisioning is in place
// but the pod is not serving SSH, and names the one remaining step — a redeploy
// this repair deliberately will not run.
func reportWorkspaceSyncSSHDMissing(ctx common.Context, result common.OpenResult, cause error) error {
	_, err := fmt.Fprintf(ctx.Stdout,
		"Installed the SSH key and wrote the ssh config, but the SSH port-forward did not come up:\n  %s\nThe runtime pod most likely has no SSHD running; run `erun sshd init %s %s` to redeploy and enable it (this repair does not redeploy).\n",
		strings.TrimSpace(cause.Error()), result.Tenant, result.Environment)
	return err
}

// persistWorkspaceSyncKeyPath resolves the SSH public key and, when the env
// config carries none, records the resolved path so later syncs and diffs share
// one key. The resolved key drives the authorized_keys install below.
func persistWorkspaceSyncKeyPath(ctx common.Context, configStore common.ConfigStore, result *common.OpenResult) error {
	publicKeyPath, _, err := resolveSSHDPublicKey(result.EnvConfig.SSHD.PublicKeyPath)
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

func writeWorkspaceSyncLocalSSHConfig(ctx common.Context, result common.OpenResult) error {
	info := common.SSHConnectionInfoForResult(result)
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("workspace-sync: write ssh config host %s for %s/%s", info.HostAlias, result.Tenant, result.Environment))
		return nil
	}
	localConfig, err := writeLocalSSHConfig(result)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(ctx.Stdout, "Wrote ssh config host %s to %s\n", localConfig.HostAlias, localConfig.ConfigPath)
	return err
}

// reportWorkspaceSyncRepairOutcome re-checks reachability and either confirms the
// mirror can now sync or names the one remaining step the repair will not run —
// a redeploy to deploy SSHD into the pod.
func reportWorkspaceSyncRepairOutcome(ctx common.Context, result common.OpenResult) error {
	info := common.SSHConnectionInfoForResult(result)
	if ctx.DryRun {
		ctx.TraceCommand("", "ssh", workspaceSyncReachArgs(info.HostAlias)...)
		return nil
	}
	if reachable, _ := probeWorkspaceSyncSSH(info.HostAlias); reachable {
		_, err := fmt.Fprintf(ctx.Stdout, "Workspace sync SSH is reachable for %s/%s; the host mirror will sync on the next pass.\n", result.Tenant, result.Environment)
		return err
	}
	_, err := fmt.Fprintf(ctx.Stdout,
		"SSH still cannot reach %s/%s. The runtime pod likely has no SSHD deployed; run `erun sshd init %s %s` to redeploy and enable it (this repair does not redeploy).\n",
		result.Tenant, result.Environment, result.Tenant, result.Environment)
	return err
}

// probeWorkspaceSyncSSH reports whether the host alias reaches the pod's SSHD,
// returning the failure detail so the diagnosis can explain what is wrong.
func probeWorkspaceSyncSSH(alias string) (bool, string) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return false, "no ssh host alias"
	}
	cmd := common.Command("ssh", workspaceSyncReachArgs(alias)...)
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
