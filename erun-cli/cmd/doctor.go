package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
	"github.com/spf13/cobra"
)

type doctorOptions struct {
	pruneImages            bool
	pruneBuildCache        bool
	pruneContainers        bool
	repairJetBrainsGateway bool
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
	return cmd
}

func runDoctorCommand(ctx common.Context, resolveOpen func(common.OpenParams) (common.OpenResult, error), promptRunner PromptRunner, options doctorOptions, args []string) error {
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
