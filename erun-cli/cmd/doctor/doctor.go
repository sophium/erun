package doctor

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/cmd/shared/clicmd"
	"github.com/sophium/erun/cmd/shared/prompt"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/doctor"
	"github.com/sophium/erun/erun-common/infra/cloud"
	diagnosispkg "github.com/sophium/erun/erun-common/primitive/deploy/diagnosis"
	"github.com/sophium/erun/erun-common/primitive/open/resolve"
	"github.com/spf13/cobra"
)

type doctorOptions struct {
	pruneImages                bool
	pruneBuildCache            bool
	pruneContainers            bool
	clearPendingHelm           bool
	rollback                   bool
	repairJetBrainsGateway     bool
	repairConfig               bool
	restoreConfigFromBackup    string
	restoreEnvConfigFromBackup string
	finishRemoteInit           bool
	remoteRepositoryURL        string
	codeCommitSSHKeyID         string
	syncConfig                 bool
	repairWorkspaceSync        bool
}

func NewDoctorCmd(resolveOpen func(model.OpenParams) (config.OpenResult, error), configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner) *cobra.Command {
	options := doctorOptions{}
	cmd := &cobra.Command{
		Use:   "doctor [tenant] [environment]",
		Short: "Diagnose and repair an environment's runtime and config",
		Long: "Diagnose and repair an environment's runtime and config.\n\n" +
			"Reports why a deploy may have failed (helm release status and the runtime pods, " +
			"read-only), and the runtime container's own CPU and memory pressure with the sizing " +
			"recommendation that answers it (the same reading and verdict `erun usage` reports, " +
			"rendered identically). A recommendation is only ever reported: the `erun resize` command " +
			"that would apply it is named, never run, because resizing rolls the runtime pod. " +
			"When the release looks unhealthy it recommends the one recovery that fits — " +
			"clear a stuck pending helm release, or roll back to the last successful revision — and " +
			"prompts before running it. It also prunes Docker images, build cache, or stopped " +
			"containers against the daemon that holds the environment's build images (the erun-dind " +
			"sidecar in its runtime pod, or this machine's docker for a host environment), naming the " +
			"daemon it pruned and what the prune freed; restores or fixes the root erun config; and " +
			"finishes an interrupted remote " +
			"init. The recovery actions mutate the live release; run one directly with --clear-pending-helm " +
			"or --rollback (the two are alternatives — pass only one). Run inside a runtime pod, " +
			"--sync-config reconciles the on-disk env config with the helm-injected ERUN_* env vars " +
			"(injected env wins) and rewrites the projected keys, preserving everything else. " +
			"Without a TTY on stdin (an MCP client, an orchestrator, a script) doctor skips the " +
			"optional prune prompts instead of blocking on them, reports each one as skipped, and " +
			"still exits on the health of what it examined; pass --prune-images, --prune-build-cache, " +
			"or --prune-containers to run one explicitly. " +
			"For a remote-agent env with workspace sync enabled it reports the host mirror's SSH " +
			"provisioning, and --repair-workspace-sync repairs it without redeploying (resolve/persist " +
			"the key, write the ssh config alias, install the pod authorized_keys, ensure the port-forward).",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := validateDoctorRecoveryFlags(options); err != nil {
				return err
			}
			return runDoctorCommand(clicmd.CommandContext(cmd), resolveOpen, configStore, cloudDeps, cloudContextDeps, promptRunner, options, args)
		},
	}
	clicmd.AddDryRunFlag(cmd)
	cmd.Flags().BoolVar(&options.pruneImages, "prune-images", false, "Prune unused Docker images without prompting")
	cmd.Flags().BoolVar(&options.pruneBuildCache, "prune-build-cache", false, "Prune unused BuildKit cache without prompting")
	cmd.Flags().BoolVar(&options.pruneContainers, "prune-containers", false, "Prune stopped Docker containers without prompting")
	cmd.Flags().BoolVar(&options.clearPendingHelm, "clear-pending-helm", false, "Clear a stuck helm pending-install/upgrade lock for the runtime release without prompting")
	cmd.Flags().BoolVar(&options.rollback, "rollback", false, "Roll the runtime release back to its last successful revision without prompting")
	cmd.Flags().BoolVar(&options.repairJetBrainsGateway, "repair-jetbrains-gateway", false, "Clear cached JetBrains Gateway backend metadata for this environment")
	cmd.Flags().BoolVar(&options.repairConfig, "repair-config", false, "Inspect the root erun config and offer to restore from backup or re-init orphaned cloud provider aliases; stops before running tenant/env cleanup actions")
	cmd.Flags().StringVar(&options.restoreConfigFromBackup, "restore-config-from-backup", "", "Restore the root erun config from a dated backup non-interactively (YYYY-MM-DD or absolute path)")
	cmd.Flags().StringVar(&options.restoreEnvConfigFromBackup, "restore-env-config-from-backup", "", "Restore the target environment's config.yaml from a dated backup (YYYY-MM-DD or absolute path); needs an explicit tenant and environment")
	cmd.Flags().BoolVar(&options.finishRemoteInit, "finish-remote-init", false, "Finish unfinished remote init tasks without prompting (only takes effect when run inside a runtime pod)")
	cmd.Flags().StringVar(&options.remoteRepositoryURL, "remote-repository-url", "", "Git remote URL to use when finishing an unfinished remote init")
	cmd.Flags().StringVar(&options.codeCommitSSHKeyID, "codecommit-ssh-key-id", "", "CodeCommit SSH public key ID to use when finishing an unfinished remote init for an AWS CodeCommit repository")
	cmd.Flags().BoolVar(&options.syncConfig, "sync-config", false, "Reconcile the in-pod erun config with the helm-injected ERUN_* env vars (only takes effect inside a runtime pod)")
	cmd.Flags().BoolVar(&options.repairWorkspaceSync, "repair-workspace-sync", false, "Repair a remote-agent env's host workspace-sync SSH provisioning (resolve/persist key, write ssh config alias, install pod authorized_keys, ensure port-forward) without redeploying the runtime")
	return cmd
}

func runDoctorCommand(ctx core.Context, resolveOpen func(model.OpenParams) (config.OpenResult, error), configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, options doctorOptions, args []string) error {
	// The host root config does not exist in the pod.
	if config.IsInRuntimeEnvironment(os.Getenv) {
		return runDoctorInRuntime(ctx, promptRunner, options)
	}

	if err := reportInstalledDesktopAppVersion(ctx); err != nil {
		return err
	}

	if done, err := runDoctorConfigRepairs(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, options, args); err != nil || done {
		return err
	}

	params, err := resolve.OpenParamsForArgs(args)
	if err != nil {
		return err
	}
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	ctx, closeEnvTrace := core.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()

	// The desktop's Local shell never sees a PTY exit, so these lines are its only completion signal.
	ctx.Info(fmt.Sprintf("==> Doctor %s/%s", result.Tenant, result.Environment))
	if err := runDoctorForTarget(ctx, configStore, promptRunner, result, options); err != nil {
		ctx.Info(fmt.Sprintf("==> Doctor failed %s/%s: %s", result.Tenant, result.Environment, err.Error()))
		return err
	}
	ctx.Info(fmt.Sprintf("==> Doctor done %s/%s", result.Tenant, result.Environment))
	return nil
}

// runDoctorForTarget reports helm and pod state first since they need no running
// pod; later pod-dependent checks degrade to "could not read" instead of aborting.
func runDoctorForTarget(ctx core.Context, configStore config.ConfigStore, promptRunner prompt.PromptRunner, result config.OpenResult, options doctorOptions) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Target: %s/%s\n", result.Tenant, result.Environment); err != nil {
		return err
	}
	if err := reportProjectConfigAndExecutionModes(ctx, result); err != nil {
		return err
	}
	req := cloud.ShellLaunchParamsFromResult(result)
	diagnosis, err := runDeployDiagnosis(ctx, req)
	if err != nil {
		return err
	}
	if err := reportDeployDiagnosisSections(ctx, configStore, result, diagnosis); err != nil {
		return err
	}
	if err := reportDoctorResources(ctx, req, result, diagnosis); err != nil {
		return err
	}
	if err := runWorkspaceSyncDoctor(ctx, promptRunner, configStore, result, options); err != nil {
		return err
	}
	if doctorOnlyRepairWorkspaceSync(options) {
		return nil
	}
	return runDoctorPostSyncActions(ctx, promptRunner, result, req, diagnosis, options)
}

// reportDeployDiagnosisSections writes the read-only deploy diagnosis sections.
func reportDeployDiagnosisSections(ctx core.Context, configStore config.ConfigStore, result config.OpenResult, diagnosis model.DeployDiagnosisResult) error {
	sections := []func() error{
		func() error { return reportRuntimeImageRegistryMismatch(ctx, result) },
		func() error { return reportRuntimeImageLineMismatch(ctx, result) },
		func() error { return reportHostCredentials(ctx, configStore, result, diagnosis) },
		func() error { return reportGitPushAccess(ctx, result, diagnosis) },
		func() error { return reportAgentCredentials(ctx, result, diagnosis) },
	}
	for _, section := range sections {
		if err := section(); err != nil {
			return err
		}
	}
	return nil
}

func reportProjectConfigAndExecutionModes(ctx core.Context, result config.OpenResult) error {
	if err := reportProjectConfigTracked(ctx, result); err != nil {
		return err
	}
	return reportExecutionModes(ctx)
}

// reportProjectConfigTracked flags a .erun/config.yaml that resolves here but is
// gitignored, so it silently falls back to defaults elsewhere.
func reportProjectConfigTracked(ctx core.Context, result config.OpenResult) error {
	if !projectConfigGitIgnored(ctx, result.RepoPath) {
		return nil
	}
	_, err := fmt.Fprintf(ctx.Stdout, "== Project config ==\n.erun/config.yaml exists but is excluded by .gitignore: it resolves on this machine only, and nowhere else. Fix: %s.\n\n", config.ProjectConfigGitIgnoreFix)
	return err
}

func projectConfigGitIgnored(ctx core.Context, repoPath string) bool {
	present, ignored, err := config.ProjectConfigGitIgnored(ctx, repoPath)
	return err == nil && present && ignored
}

func runDoctorPostSyncActions(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, req config.ShellLaunchParams, diagnosis model.DeployDiagnosisResult, options doctorOptions) error {
	repairedJetBrains, err := runSelectedJetBrainsGatewayRepair(ctx, promptRunner, result, options)
	if err != nil {
		return err
	}
	if repairedJetBrains && doctorOnlySelectedJetBrainsGatewayRepair(options) {
		return nil
	}
	return runDoctorCleanupActions(ctx, promptRunner, result, req, diagnosis, options)
}

// runDoctorConfigRepairs runs before resolveOpen, which a broken config would block.
func runDoctorConfigRepairs(ctx core.Context, configStore config.ConfigStore, cloudDeps cloud.CloudDependencies, cloudContextDeps cloud.CloudContextDependencies, promptRunner prompt.PromptRunner, options doctorOptions, args []string) (bool, error) {
	if _, err := runRootConfigDoctor(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, options); err != nil {
		return false, err
	}
	if doctorOnlyRepairConfig(options) {
		return true, nil
	}
	if selector := strings.TrimSpace(options.restoreEnvConfigFromBackup); selector != "" {
		if _, err := runEnvConfigRestoreFromArgs(ctx, args, selector); err != nil {
			return false, err
		}
		if doctorOnlyRestoreEnvConfig(options) {
			return true, nil
		}
	}
	return false, nil
}

func doctorOnlyRepairConfig(options doctorOptions) bool {
	repairOnly := options.repairConfig || strings.TrimSpace(options.restoreConfigFromBackup) != ""
	return repairOnly &&
		!options.pruneImages &&
		!options.pruneBuildCache &&
		!options.pruneContainers &&
		!options.repairJetBrainsGateway &&
		!options.finishRemoteInit
}

func doctorOnlyRestoreEnvConfig(options doctorOptions) bool {
	return strings.TrimSpace(options.restoreEnvConfigFromBackup) != "" &&
		!options.repairConfig &&
		strings.TrimSpace(options.restoreConfigFromBackup) == "" &&
		!options.pruneImages &&
		!options.pruneBuildCache &&
		!options.pruneContainers &&
		!options.repairJetBrainsGateway &&
		!options.clearPendingHelm &&
		!options.rollback &&
		!options.finishRemoteInit
}

// validateDoctorRecoveryFlags rejects both helm recoveries at once: running both
// steps the release back a revision too far.
func validateDoctorRecoveryFlags(options doctorOptions) error {
	if options.clearPendingHelm && options.rollback {
		return errors.New("--clear-pending-helm and --rollback are alternative recoveries; pass only one")
	}
	return nil
}

// runDoctorCleanupActions degrades to one "could not read" report on an unreachable
// cluster, except for a host environment's local daemon.
func runDoctorCleanupActions(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, req config.ShellLaunchParams, diagnosis model.DeployDiagnosisResult, options doctorOptions) error {
	daemon := doctor.ResolveDoctorDockerDaemon(req)
	if err := runDeployRecoveryActions(ctx, promptRunner, req, options, diagnosis); err != nil {
		return err
	}
	if diagnosis.ClusterUnreachable && daemon.Kind != model.DoctorDockerDaemonHost {
		return reportDoctorCleanupSkippedUnreachable(ctx, options)
	}
	cont, err := reportDoctorDockerStorage(ctx, req, options)
	if err != nil || !cont {
		return err
	}
	return runDoctorSelectedPrunes(ctx, promptRunner, result, req, options)
}

// reportDoctorDockerStorage names the daemon before its figures, and reports
// whether the prune actions should run (they share the same daemon).
func reportDoctorDockerStorage(ctx core.Context, req config.ShellLaunchParams, options doctorOptions) (bool, error) {
	inspection, err := doctor.RunDoctorInspection(ctx, nil, req)
	if err != nil {
		return false, reportDoctorInspectionUnreachable(ctx, options, err)
	}
	if _, err := fmt.Fprintln(ctx.Stdout, doctor.DoctorDockerDaemonLine(inspection.Daemon)); err != nil {
		return false, err
	}
	if ctx.DryRun {
		return true, nil
	}
	return true, writeDoctorCommandOutput(ctx, inspection.Stdout, inspection.Stderr)
}

func runDoctorSelectedPrunes(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, req config.ShellLaunchParams, options doctorOptions) error {
	actions, err := selectedDoctorActions(ctx, promptRunner, result, options, ctx.DryRun)
	if err != nil {
		return err
	}
	if len(actions) == 0 {
		return writeNoDoctorActionsSelected(ctx)
	}
	for _, action := range actions {
		if err := runSelectedDoctorAction(ctx, req, action); err != nil {
			return err
		}
	}
	return nil
}

// reportDoctorInspectionUnreachable reports requested prunes as skipped; with no
// build daemon at all a requested prune fails instead of implying success.
func reportDoctorInspectionUnreachable(ctx core.Context, options doctorOptions, err error) error {
	var unavailable model.DoctorDockerDaemonUnavailableError
	if errors.As(err, &unavailable) {
		if repErr := reportDoctorDockerDaemonUnavailable(ctx, err); repErr != nil {
			return repErr
		}
		if anyDoctorActionRequested(options.pruneImages, options.pruneBuildCache, options.pruneContainers) {
			return err
		}
		return nil
	}
	if repErr := reportPodUnreachable(ctx, "Docker storage", err); repErr != nil {
		return repErr
	}
	if !anyDoctorActionRequested(options.pruneImages, options.pruneBuildCache, options.pruneContainers) {
		return nil
	}
	_, ferr := fmt.Fprintln(ctx.Stdout, "Skipping the requested prune action(s) for the same reason.")
	return ferr
}

// reportDoctorDockerDaemonUnavailable avoids the retry-later wording: this fails identically every time.
func reportDoctorDockerDaemonUnavailable(ctx core.Context, err error) error {
	detail := strings.TrimSpace(err.Error())
	var unavailable model.DoctorDockerDaemonUnavailableError
	if errors.As(err, &unavailable) {
		detail = unavailable.Daemon.Report()
	}
	_, ferr := fmt.Fprintf(ctx.Stdout, "== Docker storage ==\n%s\n\n", detail)
	return ferr
}

// reportDoctorCleanupSkippedUnreachable avoids a second kubectl exec timeout once unreachability is known.
func reportDoctorCleanupSkippedUnreachable(ctx core.Context, options doctorOptions) error {
	if repErr := reportPodSkippedUnreachable(ctx, "Docker storage"); repErr != nil {
		return repErr
	}
	if !anyDoctorActionRequested(options.pruneImages, options.pruneBuildCache, options.pruneContainers) {
		return nil
	}
	_, ferr := fmt.Fprintln(ctx.Stdout, "Skipping the requested prune action(s) for the same reason.")
	return ferr
}

func writeNoDoctorActionsSelected(ctx core.Context) error {
	_, err := fmt.Fprintln(ctx.Stdout, "No cleanup actions selected.")
	return err
}

// deployDiagnosisGuidance mirrors the one-click fixes on the desktop's failed deploy card.
const deployDiagnosisGuidance = "If the release is stuck pending or an image failed to pull, re-run `erun deploy --force` to rebuild and redeploy, or clear the pending release."

// runDeployDiagnosis is read-only, so it runs under --dry-run too.
func runDeployDiagnosis(ctx core.Context, req config.ShellLaunchParams) (model.DeployDiagnosisResult, error) {
	diagnosis := diagnosispkg.RunDeployDiagnosis(ctx, req)
	if err := writeDeployDiagnosis(ctx, diagnosis); err != nil {
		return diagnosis, err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, deployDiagnosisGuidance); err != nil {
		return diagnosis, err
	}
	return diagnosis, nil
}

// runDeployRecoveryActions prompts only on an unhealthy diagnosis.
func runDeployRecoveryActions(ctx core.Context, promptRunner prompt.PromptRunner, req config.ShellLaunchParams, options doctorOptions, diagnosis model.DeployDiagnosisResult) error {
	actions, err := selectedDeployRecoveryActions(ctx, promptRunner, req, options, diagnosis, ctx.DryRun)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", doctor.DeployRecoveryActionDescription(action)); err != nil {
			return err
		}
		output, runErr := doctor.RunDeployRecovery(ctx, req, action)
		if runErr != nil {
			if detail := strings.TrimSpace(output); detail != "" {
				return fmt.Errorf("%w: %s", runErr, detail)
			}
			return runErr
		}
		if !ctx.DryRun {
			if err := writeDoctorCommandOutput(ctx, output, ""); err != nil {
				return err
			}
		}
	}
	return nil
}

// selectedDeployRecoveryActions lets explicit flags win, else offers the one recovery that fits.
func selectedDeployRecoveryActions(ctx core.Context, promptRunner prompt.PromptRunner, req config.ShellLaunchParams, options doctorOptions, diagnosis model.DeployDiagnosisResult, dryRun bool) ([]model.DeployRecoveryAction, error) {
	if options.clearPendingHelm {
		return []model.DeployRecoveryAction{model.DeployRecoveryClearPendingHelm}, nil
	}
	if options.rollback {
		return []model.DeployRecoveryAction{model.DeployRecoveryRollback}, nil
	}
	if dryRun || promptRunner == nil {
		return nil, nil
	}
	action, ok := diagnosispkg.RecommendedDeployRecovery(diagnosis)
	if !ok {
		return nil, nil
	}
	confirmed, err := doctorConfirm(ctx, promptRunner, doctor.DeployRecoveryActionPromptLabel(action, req), doctor.DeployRecoveryActionWithoutPromptHint(action))
	if err != nil {
		return nil, err
	}
	if !confirmed {
		return nil, nil
	}
	return []model.DeployRecoveryAction{action}, nil
}

func writeDeployDiagnosis(ctx core.Context, diagnosis model.DeployDiagnosisResult) error {
	if strings.TrimSpace(diagnosis.HelmStatus) != "" {
		if _, err := fmt.Fprintf(ctx.Stdout, "== Helm release status ==\n%s\n\n", diagnosis.HelmStatus); err != nil {
			return err
		}
	}
	if diagnosis.ClusterUnreachable {
		return reportPodSkippedUnreachable(ctx, "Pods")
	}
	if strings.TrimSpace(diagnosis.Pods) != "" {
		if _, err := fmt.Fprintf(ctx.Stdout, "== Pods ==\n%s\n\n", diagnosis.Pods); err != nil {
			return err
		}
	}
	return nil
}

func runSelectedDoctorAction(ctx core.Context, req config.ShellLaunchParams, action model.DoctorAction) error {
	// Name the daemon: the same action against the wrong daemon reclaims nothing.
	if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", doctor.DoctorActionDescriptionOn(action, doctor.ResolveDoctorDockerDaemon(req))); err != nil {
		return err
	}
	output, err := doctor.RunDoctorAction(ctx, nil, req, action)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	if err := writeDoctorCommandOutput(ctx, output.Stdout, output.Stderr); err != nil {
		return err
	}
	if summary := doctor.DoctorPruneSummary(output); summary != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, summary); err != nil {
			return err
		}
	}
	return nil
}

func selectedDoctorActions(ctx core.Context, promptRunner prompt.PromptRunner, result config.OpenResult, options doctorOptions, dryRun bool) ([]model.DoctorAction, error) {
	selected := make([]model.DoctorAction, 0, 3)
	if options.pruneImages {
		selected = append(selected, model.DoctorActionPruneImages)
	}
	if options.pruneBuildCache {
		selected = append(selected, model.DoctorActionPruneBuildCache)
	}
	if options.pruneContainers {
		selected = append(selected, model.DoctorActionPruneContainers)
	}
	if len(selected) > 0 || dryRun || promptRunner == nil {
		return selected, nil
	}
	// Callers without a terminal (orchestrators, CI, agents) get optional prunes
	// reported as skipped, never as a failed check.
	if !prompt.StdinIsTerminal() {
		return selected, writeOptionalDoctorPromptsSkipped(ctx, result, doctorPromptsNoTerminal)
	}
	prompted, unasked, err := promptForDoctorActions(promptRunner, result)
	if err != nil {
		return nil, err
	}
	if unasked {
		return selected, writeOptionalDoctorPromptsSkipped(ctx, result, doctorPromptsStdinClosed)
	}
	return append(selected, prompted...), nil
}

const (
	doctorPromptsNoTerminal  = "stdin is not a terminal, so there was nobody to answer the prompt"
	doctorPromptsStdinClosed = "stdin closed before the prompt could be answered"
)

// doctorConfirm treats EOF as a declined answer, naming the skipped step and its
// flag, so a missing reader does not read as an environment failure.
func doctorConfirm(ctx core.Context, promptRunner prompt.PromptRunner, label, withoutPrompt string) (bool, error) {
	confirmed, err := prompt.ConfirmPrompt(promptRunner, label)
	if !errors.Is(err, promptui.ErrEOF) {
		return confirmed, err
	}
	_, writeErr := fmt.Fprintf(ctx.Stdout, "Not run: stdin reached EOF before %q could be confirmed. %s\n",
		strings.TrimRight(strings.TrimSpace(label), "?"), withoutPrompt)
	return false, writeErr
}

// promptForDoctorActions reports unasked when the reader hit EOF.
func promptForDoctorActions(promptRunner prompt.PromptRunner, result config.OpenResult) ([]model.DoctorAction, bool, error) {
	selected := make([]model.DoctorAction, 0, len(doctor.DoctorActions()))
	for _, action := range doctor.DoctorActions() {
		ok, err := prompt.ConfirmPrompt(promptRunner, doctor.DoctorActionPromptLabel(action, result))
		if errors.Is(err, promptui.ErrEOF) {
			return nil, true, nil
		}
		if err != nil {
			return nil, false, err
		}
		if ok {
			selected = append(selected, action)
		}
	}
	return selected, false, nil
}

// writeOptionalDoctorPromptsSkipped returns only write errors; no answerer is not a failed check.
func writeOptionalDoctorPromptsSkipped(ctx core.Context, result config.OpenResult, reason string) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Optional cleanup steps in %s/%s not checked: %s. These are optional maintenance, not failed checks.\n", result.Tenant, result.Environment, reason); err != nil {
		return err
	}
	for _, action := range doctor.DoctorActions() {
		if _, err := fmt.Fprintf(ctx.Stdout, "  skipped: %s (%s)\n", action, doctor.DoctorActionDescription(action)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stdout, "Pass --prune-images, --prune-build-cache, or --prune-containers to run one explicitly.")
	return err
}

func writeDoctorCommandOutput(ctx core.Context, stdout, stderr string) error {
	stdout = strings.TrimSpace(stdout)
	stderr = strings.TrimSpace(stderr)
	if stdout != "" {
		if _, err := fmt.Fprintln(ctx.Stdout, stdout); err != nil {
			return err
		}
	}
	if stderr != "" {
		if _, err := fmt.Fprintln(ctx.Stderr, stderr); err != nil {
			return err
		}
	}
	return nil
}
