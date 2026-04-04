package cmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/manifoldco/promptui"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

const initializeCurrentProjectOption = "Initialize current project"

func NewInitCmd(deps Dependencies, verbosity *int) *cobra.Command {
	req := bootstrap.InitRequest{}

	cmd := &cobra.Command{
		Use:          "init",
		Short:        "Initialize configuration for the current project",
		Args:         cobra.MaximumNArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			request := req
			if request.Tenant == "" && len(args) > 0 {
				request.Tenant = args[0]
			}
			return runInitCommand(cmd, deps, verbosity, request)
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
		SelectTenant: func(tenants []internal.TenantConfig) (bootstrap.TenantSelectionResult, error) {
			return selectTenantPrompt(deps.SelectRunner, tenants)
		},
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

func selectTenantPrompt(run SelectRunner, tenants []internal.TenantConfig) (bootstrap.TenantSelectionResult, error) {
	items := make([]string, 0, len(tenants)+1)
	for _, tenant := range tenants {
		items = append(items, tenant.Name)
	}
	items = append(items, initializeCurrentProjectOption)

	prompt := promptui.Select{
		Label: "Select tenant",
		Items: items,
	}

	_, result, err := run(prompt)
	if err != nil {
		if errors.Is(err, promptui.ErrInterrupt) {
			return bootstrap.TenantSelectionResult{}, fmt.Errorf("tenant selection interrupted")
		}
		if errors.Is(err, promptui.ErrAbort) {
			return bootstrap.TenantSelectionResult{}, nil
		}
		return bootstrap.TenantSelectionResult{}, err
	}

	if result == initializeCurrentProjectOption {
		return bootstrap.TenantSelectionResult{Initialize: true}, nil
	}

	return bootstrap.TenantSelectionResult{Tenant: result}, nil
}
