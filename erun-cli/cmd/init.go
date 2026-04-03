package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

func NewInitCmd(deps Dependencies, verbosity *int) *cobra.Command {
	req := bootstrap.InitRequest{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize configuration for the current project",
		RunE: func(cmd *cobra.Command, args []string) error {
			service := bootstrap.Service{
				Store:           deps.Store,
				FindProjectRoot: deps.FindProjectRoot,
				Confirm: func(label string) (bool, error) {
					return confirmPrompt(deps.PromptRunner, label)
				},
			}

			result, err := service.Run(req)
			if err != nil {
				return err
			}

			cmd.Printf(
				"Initialized tenant %q with environment %q.\n",
				result.TenantConfig.Name,
				result.EnvConfig.Name,
			)
			internal.NewLogger(valueOrZero(verbosity)).Debug(result.Summary())
			return nil
		},
	}

	cmd.Flags().StringVar(&req.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&req.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&req.Environment, "environment", bootstrap.DefaultEnvironment, "Default environment name")
	cmd.Flags().BoolVarP(&req.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	return cmd
}

func valueOrZero(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func confirmPrompt(run PromptRunner, label string) (bool, error) {
	prompt := promptui.Prompt{
		Label:     label,
		IsConfirm: true,
		Default:   "y",
	}

	result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return false, fmt.Errorf("initialization interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return false, nil
		}
		return false, err
	}

	if result == "" {
		return true, nil
	}

	return strings.EqualFold(result, "y"), nil
}
