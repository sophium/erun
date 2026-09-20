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

	if err := reportInstalledDesktopAppVersion(ctx); err != nil {
		return err
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

	// The desktop pipes `erun doctor` into its shared Local shell, which never
	// produces a PTY exit (see erun-ui/AGENTS.md § "Command Completion And
	// State-Refresh Wiring"), so these are the only completion signal the
	// desktop has: an activity-queue entry to show doctor running, and a
	// recorded outcome the Manage dialog can display as the last run's result.
	ctx.Info(fmt.Sprintf("==> Doctor %s/%s", result.Tenant, result.Environment))
	if err := runDoctorForTarget(ctx, configStore, promptRunner, result, options); err != nil {
		ctx.Info(fmt.Sprintf("==> Doctor failed %s/%s: %s", result.Tenant, result.Environment, err.Error()))
		return err
	}
	ctx.Info(fmt.Sprintf("==> Doctor done %s/%s", result.Tenant, result.Environment))
	return nil
}

// runDoctorForTarget reports in the order a broken environment needs: the
// helm release and pod state first, because nothing about reading them
// requires the runtime pod to be up, and that is exactly the state most worth
// diagnosing when it is not. Every check after it that does need the pod
// (host credentials, the docker-storage inspection) degrades to a "could not
// read" report instead of aborting, so a down pod stops none of the checks
// that do not need it.
func runDoctorForTarget(ctx common.Context, configStore common.ConfigStore, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Target: %s/%s\n", result.Tenant, result.Environment); err != nil {
		return err
	}
	if err := reportProjectConfigAndExecutionModes(ctx, result); err != nil {
		return err
	}
	req := common.ShellLaunchParamsFromResult(result)
	// The gateway catalog is erun-level rather than part of this environment's
	// own config, so it is resolved from the root config here. A root config
	// that cannot be read leaves the catalog unset, which keeps the diagnosis
	// on its pre-existing answer; the root-config section above already reports
	// the unreadable config itself.
	if gateway, err := common.ResolveOpenRouterConfig(configStore); err == nil {
		req.Gateway = gateway
	}
	diagnosis, err := runDeployDiagnosis(ctx, req)
	if err != nil {
		return err
	}
	if err := reportDeployDiagnosisSections(ctx, configStore, result, diagnosis); err != nil {
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

// reportDeployDiagnosisSections writes the read-only sections that report what
// the deploy diagnosis found: the environment's image and registry
// configuration, the credentials injected into its runtime pod, and whether
// that pod carries the model-provider wiring it was deployed with. They read as
// one sequence because they share a shape -- each states one fact about the
// environment as deployed, and none of them mutates anything -- and they are
// grouped here rather than inlined so the section list stays a list.
func reportDeployDiagnosisSections(ctx common.Context, configStore common.ConfigStore, result common.OpenResult, diagnosis common.DeployDiagnosisResult) error {
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

// reportProjectConfigAndExecutionModes runs the two checks that need neither
// req nor a deploy diagnosis, ahead of everything that does.
func reportProjectConfigAndExecutionModes(ctx common.Context, result common.OpenResult) error {
	if err := reportProjectConfigTracked(ctx, result); err != nil {
		return err
	}
	return reportExecutionModes(ctx)
}

// reportProjectConfigTracked reports when this environment's project config
// (.erun/config.yaml) exists but is excluded by .gitignore -- a silent
// onboarding failure: it resolves fine on this machine and nowhere else,
// because git never sees it, so the same build/deploy elsewhere falls back to
// unconfigured defaults with no clue why. A detection failure (no git
// repository, no config file at all) is not reported; only a config that
// resolves yet is untracked is worth flagging here.
func reportProjectConfigTracked(ctx common.Context, result common.OpenResult) error {
	present, ignored, err := common.ProjectConfigGitIgnored(ctx, result.RepoPath)
	if err != nil || !present || !ignored {
		return nil
	}
	_, err = fmt.Fprintf(ctx.Stdout, "== Project config ==\n.erun/config.yaml exists but is excluded by .gitignore: it resolves on this machine only, and nowhere else. Fix: %s.\n\n", common.ProjectConfigGitIgnoreFix)
	return err
}

// runDoctorPostSyncActions runs the JetBrains Gateway repair and then the
// remaining cleanup actions, unless the JetBrains repair was the only action
// requested. It is the tail of runDoctorCommand's diagnosis sequence.
func runDoctorPostSyncActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, req common.ShellLaunchParams, diagnosis common.DeployDiagnosisResult, options doctorOptions) error {
	repairedJetBrains, err := runSelectedJetBrainsGatewayRepair(ctx, promptRunner, result, options)
	if err != nil {
		return err
	}
	if repairedJetBrains && doctorOnlySelectedJetBrainsGatewayRepair(options) {
		return nil
	}
	return runDoctorCleanupActions(ctx, promptRunner, result, req, diagnosis, options)
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

// runDoctorCleanupActions runs the mutating helm-level recovery (if the
// diagnosis recommends one and the operator confirms it), then the
// docker-storage inspection and any requested prune actions. The inspection
// and the prune actions all exec into the runtime pod's dind container, so a
// pod that cannot be reached degrades this whole tail to a single "could not
// read" report rather than one of them aborting with a raw exec error —
// there is nothing left to run here that does not need the same pod.
func runDoctorCleanupActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, req common.ShellLaunchParams, diagnosis common.DeployDiagnosisResult, options doctorOptions) error {
	if err := runDeployRecoveryActions(ctx, promptRunner, req, options, diagnosis); err != nil {
		return err
	}
	if diagnosis.ClusterUnreachable {
		return reportDoctorCleanupSkippedUnreachable(ctx, options)
	}
	inspection, err := common.RunDoctorInspection(ctx, nil, req)
	if err != nil {
		return reportDoctorInspectionUnreachable(ctx, options, err)
	}
	if !ctx.DryRun {
		if err := writeDoctorCommandOutput(ctx, inspection.Stdout, inspection.Stderr); err != nil {
			return err
		}
	}

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

// reportDoctorInspectionUnreachable degrades the docker-storage inspection
// failure to a report and, when the operator requested a prune action that
// execs into the same unreachable dind container, says plainly that it was
// skipped rather than attempting it and failing with the same cause again.
func reportDoctorInspectionUnreachable(ctx common.Context, options doctorOptions, err error) error {
	if repErr := reportPodUnreachable(ctx, "Docker storage", err); repErr != nil {
		return repErr
	}
	if !anyDoctorActionRequested(options.pruneImages, options.pruneBuildCache, options.pruneContainers) {
		return nil
	}
	_, ferr := fmt.Fprintln(ctx.Stdout, "Skipping the requested prune action(s) for the same reason.")
	return ferr
}

// reportDoctorCleanupSkippedUnreachable mirrors reportDoctorInspectionUnreachable
// for the case where an earlier section already confirmed the cluster is
// unreachable: it reports the same skip and the same "no prune action ran"
// outcome without paying a second kubectl exec timeout to rediscover what the
// helm release status section already proved.
func reportDoctorCleanupSkippedUnreachable(ctx common.Context, options doctorOptions) error {
	if repErr := reportPodSkippedUnreachable(ctx, "Docker storage"); repErr != nil {
		return repErr
	}
	if !anyDoctorActionRequested(options.pruneImages, options.pruneBuildCache, options.pruneContainers) {
		return nil
	}
	_, ferr := fmt.Fprintln(ctx.Stdout, "Skipping the requested prune action(s) for the same reason.")
	return ferr
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
// actions. It runs under --dry-run too: the flag withholds mutations, not
// reads, and this diagnosis is the whole reason an operator reaches for it.
func runDeployDiagnosis(ctx common.Context, req common.ShellLaunchParams) (common.DeployDiagnosisResult, error) {
	diagnosis := common.RunDeployDiagnosis(ctx, req)
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
	actions, err := selectedDeployRecoveryActions(ctx, promptRunner, req, options, diagnosis, ctx.DryRun)
	if err != nil {
		return err
	}
	for _, action := range actions {
		if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", common.DeployRecoveryActionDescription(action)); err != nil {
			return err
		}
		output, runErr := common.RunDeployRecovery(ctx, req, action)
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

// selectedDeployRecoveryActions resolves which recovery to run: explicit flags
// win, else the interactive path offers a single confirm for the one recovery
// that fits the diagnosis — clearing a pending lock and rolling back are
// alternative fixes, and running both is wrong.
func selectedDeployRecoveryActions(ctx common.Context, promptRunner PromptRunner, req common.ShellLaunchParams, options doctorOptions, diagnosis common.DeployDiagnosisResult, dryRun bool) ([]common.DeployRecoveryAction, error) {
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
	confirmed, err := doctorConfirm(ctx, promptRunner, common.DeployRecoveryActionPromptLabel(action, req), common.DeployRecoveryActionWithoutPromptHint(action))
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
	return doctorConfirm(ctx, promptRunner, fmt.Sprintf("Clear cached JetBrains Gateway backend metadata for %s/%s?", result.Tenant, result.Environment),
		"Re-run with --repair-jetbrains-gateway to run it without a prompt.")
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

func selectedDoctorActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions, dryRun bool) ([]common.DoctorAction, error) {
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
	// doctor is the command reached for when a deploy has already failed, which
	// is exactly when the caller is an orchestrator, a CI job, or an agent — none
	// of which have a terminal to answer a prompt on. These prunes are optional
	// maintenance with explicit flags of their own, so with no terminal they are
	// skipped and named as skipped, never turned into a failed check: a
	// diagnostic that reports "environment broken" because nobody answered an
	// optional prompt is worse than one that says what it did not do.
	if !stdinIsTerminal() {
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

// The two reasons the optional prompts went unasked differ in cause and so in
// what the reader should do about them, and the report says which one happened
// rather than prescribing a fix for the wrong one.
const (
	doctorPromptsNoTerminal  = "stdin is not a terminal, so there was nobody to answer the prompt"
	doctorPromptsStdinClosed = "stdin closed before the prompt could be answered"
)

// doctorConfirm answers a confirm doctor offers for a step it cannot simply
// skip, such as a recovery that mutates the live release. A reader that went
// away is a declined answer -- the default these prompts already document --
// and not a diagnosis: letting EOF surface as an error turns it into "Doctor
// failed <tenant>/<env>", which reads as a verdict on an environment nothing
// was wrong with, to the caller that never got the report it asked for. The
// line names the step that was not run and how to run it without a prompt, so
// the caller still has a next action. A real prompt failure still propagates.
func doctorConfirm(ctx common.Context, promptRunner PromptRunner, label, withoutPrompt string) (bool, error) {
	confirmed, err := confirmPrompt(promptRunner, label)
	if !errors.Is(err, promptui.ErrEOF) {
		return confirmed, err
	}
	_, writeErr := fmt.Fprintf(ctx.Stdout, "Not run: stdin reached EOF before %q could be confirmed. %s\n",
		strings.TrimRight(strings.TrimSpace(label), "?"), withoutPrompt)
	return false, writeErr
}

// promptForDoctorActions asks about each optional prune action. unasked reports
// that the reader hit EOF instead of answering: a terminal that closed mid-run
// is still nobody declining optional maintenance, so the caller reports those
// actions as skipped rather than failing the diagnosis over them.
func promptForDoctorActions(promptRunner PromptRunner, result common.OpenResult) ([]common.DoctorAction, bool, error) {
	selected := make([]common.DoctorAction, 0, len(common.DoctorActions()))
	for _, action := range common.DoctorActions() {
		ok, err := confirmPrompt(promptRunner, common.DoctorActionPromptLabel(action, result))
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

// writeOptionalDoctorPromptsSkipped names each optional prune action that went
// unasked and says plainly that it was skipped rather than run or failed. It
// returns only write errors: a caller that could not be asked anything is not a
// failed check, and doctor's exit code must reflect the environment's health,
// not the absence of somebody to answer a prompt.
func writeOptionalDoctorPromptsSkipped(ctx common.Context, result common.OpenResult, reason string) error {
	if _, err := fmt.Fprintf(ctx.Stdout, "Optional cleanup steps in %s/%s not checked: %s. These are optional maintenance, not failed checks.\n", result.Tenant, result.Environment, reason); err != nil {
		return err
	}
	for _, action := range common.DoctorActions() {
		if _, err := fmt.Fprintf(ctx.Stdout, "  skipped: %s (%s)\n", action, common.DoctorActionDescription(action)); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(ctx.Stdout, "Pass --prune-images, --prune-build-cache, or --prune-containers to run one explicitly.")
	return err
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
	return doctorConfirm(ctx, promptRunner, "Finish missing remote-init steps now",
		"Re-run with --finish-remote-init to run it without a prompt.")
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
