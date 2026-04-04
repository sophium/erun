package cmd

import (
	"github.com/manifoldco/promptui"
	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/bootstrap"
	"github.com/spf13/cobra"
)

type PromptRunner func(promptui.Prompt) (string, error)

type Dependencies struct {
	Store           bootstrap.Store
	FindProjectRoot bootstrap.ProjectFinder
	PromptRunner    PromptRunner
}

func DefaultDependencies() Dependencies {
	return Dependencies{
		Store:           bootstrap.ConfigStore{},
		FindProjectRoot: internal.FindProjectRoot,
		PromptRunner:    defaultPromptRunner,
	}
}

var defaultPromptRunner = func(prompt promptui.Prompt) (string, error) {
	return prompt.Run()
}

func NewRootCmd(deps Dependencies) *cobra.Command {
	deps = withDependencyDefaults(deps)
	var verbosity int

	cmd := &cobra.Command{
		Use:           "erun",
		Short:         "Environment Runner",
		Long:          `erun helps to run and manage multiple tenants/environments.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runInitCommand(cmd, deps, &verbosity, bootstrap.InitRequest{})
		},
	}

	cmd.PersistentFlags().CountVarP(&verbosity, "verbose", "v", "Increase logging verbosity. Repeat for more detail.")
	cmd.AddCommand(NewInitCmd(deps, &verbosity))
	cmd.AddCommand(NewMCPCmd(deps))
	cmd.AddCommand(NewVersionCmd())
	return cmd
}

func Execute() error {
	return NewRootCmd(DefaultDependencies()).Execute()
}

func withDependencyDefaults(deps Dependencies) Dependencies {
	if deps.Store == nil {
		deps.Store = bootstrap.ConfigStore{}
	}
	if deps.FindProjectRoot == nil {
		deps.FindProjectRoot = internal.FindProjectRoot
	}
	if deps.PromptRunner == nil {
		deps.PromptRunner = defaultPromptRunner
	}
	return deps
}
