package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

func NewInitCmd(deps Dependencies, verbosity *int) *cobra.Command {
	req := bootstrap.InitRequest{}

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize configuration for the current project",
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInitCommand(cmd, deps, verbosity, req)
		},
	}

	cmd.Flags().StringVar(&req.Tenant, "tenant", "", "Tenant name to initialize")
	cmd.Flags().StringVar(&req.ProjectRoot, "project-root", "", "Project root to bind to the tenant")
	cmd.Flags().StringVar(&req.Environment, "environment", "", "Default environment name")
	cmd.Flags().BoolVarP(&req.AutoApprove, "yes", "y", false, "Automatically approve initialization prompts")
	return cmd
}

func runInitCommand(cmd *cobra.Command, deps Dependencies, verbosity *int, req bootstrap.InitRequest) error {
	logger := eruncommon.NewLoggerWithWriters(valueOrZero(verbosity), cmd.OutOrStdout(), cmd.ErrOrStderr())
	service := bootstrap.Service{
		Store:           deps.Store,
		FindProjectRoot: deps.FindProjectRoot,
		Confirm: func(label string) (bool, error) {
			return confirmPrompt(deps.PromptRunner, label)
		},
		Logger: logger,
	}

	if _, err := service.Run(req); err != nil {
		return err
	}
	return nil
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
