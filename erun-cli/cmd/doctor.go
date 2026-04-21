package cmd

import (
	"fmt"
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

type doctorOptions struct {
	pruneImages     bool
	pruneBuildCache bool
	pruneContainers bool
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
		if ctx.DryRun {
			_, err := fmt.Fprintln(ctx.Stdout, "No cleanup actions selected.")
			return err
		}
		if _, err := fmt.Fprintln(ctx.Stdout, "No cleanup actions selected."); err != nil {
			return err
		}
		return nil
	}

	for _, action := range actions {
		if _, err := fmt.Fprintf(ctx.Stdout, "Running: %s\n", common.DoctorActionDescription(action)); err != nil {
			return err
		}
		output, err := common.RunDoctorAction(ctx, nil, req, action)
		if err != nil {
			return err
		}
		if !ctx.DryRun {
			if err := writeDoctorCommandOutput(ctx, output.Stdout, output.Stderr); err != nil {
				return err
			}
		}
	}
	return nil
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
