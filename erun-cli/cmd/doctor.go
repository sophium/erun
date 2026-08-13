package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/manifoldco/promptui"
	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
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

type jetBrainsGatewayDoctorRepair struct {
	optionsDir  string
	configID    string
	projectPath string
	idePath     string
}

func newDoctorCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner) *cobra.Command {
	options := doctorOptions{}
	cmd := &cobra.Command{
		Use:   "doctor [tenant] [environment]",
		Short: "Diagnose and repair an environment's runtime and config",
		Long: "Diagnose and repair an environment's runtime and config.\n\n" +
			"Reports why a deploy may have failed (helm release status and the runtime pods, " +
			"read-only). When the release looks unhealthy it recommends the one recovery that fits — " +
			"clear a stuck pending helm release, or roll back to the last successful revision — and " +
			"prompts before running it. It also prunes Docker images, build cache, or stopped " +
			"containers; restores or fixes the root erun config; and finishes an interrupted remote " +
			"init. The recovery actions mutate the live release; run one directly with --clear-pending-helm " +
			"or --rollback (the two are alternatives — pass only one). Run inside a runtime pod, " +
			"--sync-config reconciles the on-disk env config with the helm-injected ERUN_* env vars " +
			"(injected env wins) and rewrites the projected keys, preserving everything else. " +
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
			return runDoctorCommand(commandContext(cmd), resolveOpen, configStore, cloudDeps, cloudContextDeps, promptRunner, options, args)
		},
	}
	addDryRunFlag(cmd)
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

func runDoctorCommand(ctx common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, options doctorOptions, args []string) error {
	// In-pod invocations target remote-init recovery instead; the host root
	// config the checks below would inspect does not exist in the pod.
	if common.IsInRuntimeEnvironment(os.Getenv) {
		return runDoctorInRuntime(ctx, promptRunner, options)
	}

	if done, err := runDoctorConfigRepairs(ctx, configStore, cloudDeps, cloudContextDeps, promptRunner, options, args); err != nil || done {
		return err
	}

	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return err
	}
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}
	ctx, closeEnvTrace := common.ActivateEnvTrace(ctx, result.Tenant, result.Environment)
	defer closeEnvTrace()

	if _, err := fmt.Fprintf(ctx.Stdout, "Target: %s/%s\n", result.Tenant, result.Environment); err != nil {
		return err
	}
	if err := reportHostCredentials(ctx, configStore, result); err != nil {
		return err
	}
	if err := runWorkspaceSyncDoctor(ctx, promptRunner, configStore, result, options); err != nil {
		return err
	}
	if doctorOnlyRepairWorkspaceSync(options) {
		return nil
	}
	return runDoctorPostSyncActions(ctx, promptRunner, result, options)
}

// runDoctorPostSyncActions runs the JetBrains Gateway repair and then the
// remaining cleanup actions, unless the JetBrains repair was the only action
// requested. It is the tail of runDoctorCommand's diagnosis sequence.
func runDoctorPostSyncActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) error {
	repairedJetBrains, err := runSelectedJetBrainsGatewayRepair(ctx, promptRunner, result, options)
	if err != nil {
		return err
	}
	if repairedJetBrains && doctorOnlySelectedJetBrainsGatewayRepair(options) {
		return nil
	}
	return runDoctorCleanupActions(ctx, promptRunner, result, options)
}

// runDoctorConfigRepairs runs the host-side config recoveries before
// resolveOpen: a broken root config (the motivating failure mode — missing
// CloudProviders that block resolveOpen) or a corrupted per-env config would
// otherwise block the resolve the rest of doctor relies on.
func runDoctorConfigRepairs(ctx common.Context, configStore common.ConfigStore, cloudDeps common.CloudDependencies, cloudContextDeps common.CloudContextDependencies, promptRunner PromptRunner, options doctorOptions, args []string) (bool, error) {
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

// validateDoctorRecoveryFlags rejects asking for both helm-level recoveries at
// once: clearing a pending lock and rolling back are alternative fixes, and
// running both in one invocation steps the release back a revision too far.
func validateDoctorRecoveryFlags(options doctorOptions) error {
	if options.clearPendingHelm && options.rollback {
		return errors.New("--clear-pending-helm and --rollback are alternative recoveries; pass only one")
	}
	return nil
}

func runDoctorCleanupActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) error {
	req := common.ShellLaunchParamsFromResult(result)
	diagnosis, err := runDeployDiagnosis(ctx, req)
	if err != nil {
		return err
	}
	if err := runDeployRecoveryActions(ctx, promptRunner, req, options, diagnosis); err != nil {
		return err
	}
	inspection, err := common.RunDoctorInspection(ctx, nil, req)
	if err != nil {
		return err
	}
	if !ctx.DryRun {
		if err := writeDoctorCommandOutput(ctx, inspection.Stdout, inspection.Stderr); err != nil {
			return err
		}
	}

	actions, err := selectedDoctorActions(promptRunner, result, options, ctx.DryRun)
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

func writeNoDoctorActionsSelected(ctx common.Context) error {
	_, err := fmt.Fprintln(ctx.Stdout, "No cleanup actions selected.")
	return err
}

// deployDiagnosisGuidance points at fixes for the common deploy-failure modes;
// the desktop Activities panel exposes these same fixes as one-click buttons on
// a failed deploy card.
const deployDiagnosisGuidance = "If the release is stuck pending or an image failed to pull, re-run `erun deploy --force` to rebuild and redeploy, or clear the pending release."

// runDeployDiagnosis reports helm release status and pods, read-only, so the
// reader sees why a deploy failed before deciding on the destructive recovery
// actions.
func runDeployDiagnosis(ctx common.Context, req common.ShellLaunchParams) (common.DeployDiagnosisResult, error) {
	diagnosis := common.RunDeployDiagnosis(ctx, req)
	if ctx.DryRun {
		return diagnosis, nil
	}
	if err := writeDeployDiagnosis(ctx, diagnosis); err != nil {
		return diagnosis, err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, deployDiagnosisGuidance); err != nil {
		return diagnosis, err
	}
	return diagnosis, nil
}

// runDeployRecoveryActions runs the selected helm-level recovery. The actions
// mutate the live release, so prompts are gated on an unhealthy diagnosis —
// `erun doctor` never offers rollback on a healthy env.
func runDeployRecoveryActions(ctx common.Context, promptRunner PromptRunner, req common.ShellLaunchParams, options doctorOptions, diagnosis common.DeployDiagnosisResult) error {
	actions, err := selectedDeployRecoveryActions(promptRunner, req, options, diagnosis, ctx.DryRun)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", common.DeployRecoveryActionDescription(action)); err != nil {
			return err
		}
		output, runErr := common.RunDeployRecovery(ctx, req, action)
		if runErr != nil {
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

// selectedDeployRecoveryActions resolves which recovery to run: explicit flags
// win, else the interactive path offers a single confirm for the one recovery
// that fits the diagnosis — clearing a pending lock and rolling back are
// alternative fixes, and running both is wrong.
func selectedDeployRecoveryActions(promptRunner PromptRunner, req common.ShellLaunchParams, options doctorOptions, diagnosis common.DeployDiagnosisResult, dryRun bool) ([]common.DeployRecoveryAction, error) {
	if options.clearPendingHelm {
		return []common.DeployRecoveryAction{common.DeployRecoveryClearPendingHelm}, nil
	}
	if options.rollback {
		return []common.DeployRecoveryAction{common.DeployRecoveryRollback}, nil
	}
	if dryRun || promptRunner == nil {
		return nil, nil
	}
	action, ok := common.RecommendedDeployRecovery(diagnosis)
	if !ok {
		return nil, nil
	}
	confirmed, err := confirmPrompt(promptRunner, common.DeployRecoveryActionPromptLabel(action, req))
	if err != nil {
		return nil, err
	}
	if !confirmed {
		return nil, nil
	}
	return []common.DeployRecoveryAction{action}, nil
}

func writeDeployDiagnosis(ctx common.Context, diagnosis common.DeployDiagnosisResult) error {
	if strings.TrimSpace(diagnosis.HelmStatus) != "" {
		if _, err := fmt.Fprintf(ctx.Stdout, "== Helm release status ==\n%s\n\n", diagnosis.HelmStatus); err != nil {
			return err
		}
	}
	if strings.TrimSpace(diagnosis.Pods) != "" {
		if _, err := fmt.Fprintf(ctx.Stdout, "== Pods ==\n%s\n\n", diagnosis.Pods); err != nil {
			return err
		}
	}
	return nil
}

func runSelectedDoctorAction(ctx common.Context, req common.ShellLaunchParams, action common.DoctorAction) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", common.DoctorActionDescription(action)); err != nil {
		return err
	}
	output, err := common.RunDoctorAction(ctx, nil, req, action)
	if err != nil {
		return err
	}
	if ctx.DryRun {
		return nil
	}
	return writeDoctorCommandOutput(ctx, output.Stdout, output.Stderr)
}

func runSelectedJetBrainsGatewayRepair(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) (bool, error) {
	repair, ok, err := jetBrainsGatewayDoctorRepairForResult(result)
	if err != nil {
		return false, err
	}
	if !ok {
		if options.repairJetBrainsGateway {
			_, err := fmt.Fprintln(ctx.Stdout, "No cached JetBrains Gateway backend metadata found for this environment.")
			return true, err
		}
		return false, nil
	}

	selected, err := shouldRepairJetBrainsGateway(ctx, promptRunner, result, options)
	if err != nil {
		return false, err
	}
	if !selected {
		return false, nil
	}
	return runJetBrainsGatewayRepair(ctx, repair)
}

func shouldRepairJetBrainsGateway(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) (bool, error) {
	if options.repairJetBrainsGateway {
		return true, nil
	}
	if promptRunner == nil || ctx.DryRun {
		return false, nil
	}
	return confirmPrompt(promptRunner, fmt.Sprintf("Clear cached JetBrains Gateway backend metadata for %s/%s?", result.Tenant, result.Environment))
}

func runJetBrainsGatewayRepair(ctx common.Context, repair jetBrainsGatewayDoctorRepair) (bool, error) {
	if _, err := fmt.Fprintf(ctx.Stdout, "Running: Clear cached JetBrains Gateway backend metadata\n"); err != nil {
		return false, err
	}
	if _, err := fmt.Fprintf(ctx.Stdout, "Cached backend path: %s\n", repair.idePath); err != nil {
		return false, err
	}
	if ctx.DryRun {
		_, err := fmt.Fprintf(ctx.Stdout, "Would clear latest used IDE metadata in %s\n", repair.optionsDir)
		return true, err
	}
	changed, err := jetbrainsconfig.ClearRecentProjectLatestUsedIDE(repair.optionsDir, repair.configID, repair.projectPath)
	if err != nil {
		return false, err
	}
	if !changed {
		_, err := fmt.Fprintln(ctx.Stdout, "No JetBrains Gateway metadata changed.")
		return true, err
	}
	_, err = fmt.Fprintln(ctx.Stdout, "Cleared cached JetBrains Gateway backend metadata. Open IntelliJ again to let Gateway select or redeploy the backend.")
	return true, err
}

func jetBrainsGatewayDoctorRepairForResult(result common.OpenResult) (jetBrainsGatewayDoctorRepair, bool, error) {
	optionsDir, err := resolveIntelliJOptionsDir(currentHostOS())
	if err != nil {
		return jetBrainsGatewayDoctorRepair{}, false, nil
	}
	info := common.SSHConnectionInfoForResult(result)
	configID := jetbrainsconfig.StableConfigID(info.HostAlias)
	projectPath := strings.TrimSpace(info.WorkspacePath)
	recent, found, err := jetbrainsconfig.FindRecentProject(optionsDir, configID, projectPath)
	if err != nil {
		return jetBrainsGatewayDoctorRepair{}, false, err
	}
	idePath := strings.TrimSpace(recent.LatestUsedIDE.PathToIDE)
	if !found || idePath == "" {
		return jetBrainsGatewayDoctorRepair{}, false, nil
	}
	return jetBrainsGatewayDoctorRepair{
		optionsDir:  optionsDir,
		configID:    configID,
		projectPath: projectPath,
		idePath:     idePath,
	}, true, nil
}

func doctorOnlySelectedJetBrainsGatewayRepair(options doctorOptions) bool {
	return options.repairJetBrainsGateway && !options.pruneImages && !options.pruneBuildCache && !options.pruneContainers
}

func selectedDoctorActions(promptRunner PromptRunner, result common.OpenResult, options doctorOptions, dryRun bool) ([]common.DoctorAction, error) {
	selected := make([]common.DoctorAction, 0, 3)
	if options.pruneImages {
		selected = append(selected, common.DoctorActionPruneImages)
	}
	if options.pruneBuildCache {
		selected = append(selected, common.DoctorActionPruneBuildCache)
	}
	if options.pruneContainers {
		selected = append(selected, common.DoctorActionPruneContainers)
	}
	if len(selected) > 0 || dryRun || promptRunner == nil {
		return selected, nil
	}

	for _, action := range common.DoctorActions() {
		ok, err := confirmPrompt(promptRunner, common.DoctorActionPromptLabel(action, result))
		if err != nil {
			return nil, err
		}
		if ok {
			selected = append(selected, action)
		}
	}
	return selected, nil
}

func runDoctorInRuntime(ctx common.Context, promptRunner PromptRunner, options doctorOptions) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	// --sync-config short-circuits before remote-init: config drift is more
	// fundamental (a wrong type mis-drives everything downstream), and gating it
	// here leaves plain in-pod `erun doctor` output byte-for-byte unchanged.
	if options.syncConfig {
		return runRuntimeConfigSync(ctx, promptRunner, options, common.ResolveRuntimeConfigHome(homeDir))
	}
	inspection, err := common.InspectRemoteInit(homeDir, os.Getenv)
	if err != nil {
		return err
	}
	if err := common.WriteRemoteInitInspectionReport(ctx, inspection); err != nil {
		return err
	}
	if handled, err := writeRemoteInitShortCircuit(ctx, inspection); handled || err != nil {
		return err
	}
	proceed, err := confirmRemoteInitFinish(ctx, promptRunner, options)
	if err != nil || !proceed {
		return err
	}
	updated, err := common.RunRemoteInitFinish(ctx, inspection, common.RemoteInitFinishParams{
		HomeDir:            homeDir,
		RepositoryURL:      options.remoteRepositoryURL,
		CodeCommitSSHKeyID: options.codeCommitSSHKeyID,
		Sleep:              time.Sleep,
	}, remoteInitPromptFunc(promptRunner))
	if err != nil {
		return err
	}
	return writeRemoteInitFinishReport(ctx, updated)
}

func writeRemoteInitShortCircuit(ctx common.Context, inspection common.RemoteInitInspection) (bool, error) {
	if inspection.Complete() {
		_, err := fmt.Fprintln(ctx.Stdout, "Remote init is complete; nothing to finish.")
		return true, err
	}
	if len(inspection.MissingItems()) == 0 {
		_, err := fmt.Fprintln(ctx.Stdout, "No missing remote-init artifacts detected, but the bootstrap marker is incomplete. Re-run `erun init --remote` from your local machine to refresh the marker.")
		return true, err
	}
	return false, nil
}

func confirmRemoteInitFinish(ctx common.Context, promptRunner PromptRunner, options doctorOptions) (bool, error) {
	if options.finishRemoteInit {
		return true, nil
	}
	if ctx.DryRun || promptRunner == nil {
		_, err := fmt.Fprintln(ctx.Stdout, "Run `erun doctor --finish-remote-init` inside this pod to finish the missing steps.")
		return false, err
	}
	return confirmPrompt(promptRunner, "Finish missing remote-init steps now")
}

func remoteInitPromptFunc(promptRunner PromptRunner) common.RemoteInitFinishPrompt {
	return func(label string) (string, error) {
		if promptRunner == nil {
			return "", errors.New("interactive prompt is unavailable; pass --remote-repository-url and --codecommit-ssh-key-id when applicable")
		}
		return doctorRemoteInitPrompt(promptRunner, label)
	}
}

func writeRemoteInitFinishReport(ctx common.Context, updated common.RemoteInitInspection) error {
	if ctx.DryRun {
		_, err := fmt.Fprintln(ctx.Stdout, "Dry-run: would finish remote init by running the steps traced above.")
		return err
	}
	if _, err := fmt.Fprintln(ctx.Stdout, "Remote init finished."); err != nil {
		return err
	}
	return common.WriteRemoteInitInspectionReport(ctx, updated)
}

func doctorRemoteInitPrompt(run PromptRunner, label string) (string, error) {
	prompt := promptui.Prompt{
		Label: label,
		Validate: func(input string) error {
			if strings.TrimSpace(input) == "" {
				return fmt.Errorf("%s is required", label)
			}
			return nil
		},
	}
	result, err := run(prompt)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(result), nil
}

func writeDoctorCommandOutput(ctx common.Context, stdout, stderr string) error {
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
