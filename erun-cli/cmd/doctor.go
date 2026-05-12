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
	pruneImages            bool
	pruneBuildCache        bool
	pruneContainers        bool
	repairJetBrainsGateway bool
	finishRemoteInit       bool
	remoteRepositoryURL    string
	codeCommitSSHKeyID     string
}

type jetBrainsGatewayDoctorRepair struct {
	optionsDir  string
	configID    string
	projectPath string
	idePath     string
}

func newDoctorCmd(resolveOpen func(common.OpenParams) (common.OpenResult, error), promptRunner PromptRunner) *cobra.Command {
	options := doctorOptions{}
	cmd := &cobra.Command{
		Use:           "doctor [tenant] [environment]",
		Short:         "Inspect the DevOps runtime and offer Docker cleanup actions",
		Args:          cobra.MaximumNArgs(2),
		SilenceErrors: true,
		SilenceUsage:  true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctorCommand(commandContext(cmd), resolveOpen, promptRunner, options, args)
		},
	}
	addDryRunFlag(cmd)
	cmd.Flags().BoolVar(&options.pruneImages, "prune-images", false, "Prune unused Docker images without prompting")
	cmd.Flags().BoolVar(&options.pruneBuildCache, "prune-build-cache", false, "Prune unused BuildKit cache without prompting")
	cmd.Flags().BoolVar(&options.pruneContainers, "prune-containers", false, "Prune stopped Docker containers without prompting")
	cmd.Flags().BoolVar(&options.repairJetBrainsGateway, "repair-jetbrains-gateway", false, "Clear cached JetBrains Gateway backend metadata for this environment")
	cmd.Flags().BoolVar(&options.finishRemoteInit, "finish-remote-init", false, "Finish unfinished remote init tasks without prompting (only takes effect when run inside a runtime pod)")
	cmd.Flags().StringVar(&options.remoteRepositoryURL, "remote-repository-url", "", "Git remote URL to use when finishing an unfinished remote init")
	cmd.Flags().StringVar(&options.codeCommitSSHKeyID, "codecommit-ssh-key-id", "", "CodeCommit SSH public key ID to use when finishing an unfinished remote init for an AWS CodeCommit repository")
	return cmd
}

func runDoctorCommand(ctx common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), promptRunner PromptRunner, options doctorOptions, args []string) error {
	if common.IsInRuntimeEnvironment(os.Getenv) {
		return runDoctorInRuntime(ctx, promptRunner, options)
	}
	params, err := common.OpenParamsForArgs(args)
	if err != nil {
		return err
	}
	result, err := resolveOpen(params)
	if err != nil {
		return err
	}

	if _, err := fmt.Fprintf(ctx.Stdout, "Target: %s/%s\n", result.Tenant, result.Environment); err != nil {
		return err
	}
	repairedJetBrains, err := runSelectedJetBrainsGatewayRepair(ctx, promptRunner, result, options)
	if err != nil {
		return err
	}
	if repairedJetBrains && doctorOnlySelectedJetBrainsGatewayRepair(options) {
		return nil
	}
	return runDoctorCleanupActions(ctx, promptRunner, result, options)
}

func runDoctorCleanupActions(ctx common.Context, promptRunner PromptRunner, result common.OpenResult, options doctorOptions) error {
	req := common.ShellLaunchParamsFromResult(result)
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
