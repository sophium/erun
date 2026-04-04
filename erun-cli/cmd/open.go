package cmd

import (
	"errors"

	"github.com/sophium/erun/internal"
	"github.com/sophium/erun/internal/opener"
	"github.com/spf13/cobra"
)

func NewOpenCmd(deps Dependencies) *cobra.Command {
	return &cobra.Command{
		Use:          "open TENANT ENVIRONMENT",
		Short:        "Open a shell in the tenant environment worktree",
		Args:         cobra.ExactArgs(2),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOpenCommand(deps, opener.Request{
				Tenant:      args[0],
				Environment: args[1],
			})
		},
	}
}

func runOpenCommand(deps Dependencies, req opener.Request) error {
	_, err := newOpenService(deps).Run(req)
	return err
}

func resolveOpenCommand(deps Dependencies, req opener.Request) (opener.Result, error) {
	return newOpenService(deps).Resolve(req)
}

func launchOpenResult(deps Dependencies, result opener.Result) error {
	launcher := deps.LaunchShell
	if launcher == nil {
		launcher = opener.DefaultShellLauncher
	}
	return launcher(opener.ShellLaunchRequest{
		Dir:   result.RepoPath,
		Title: result.Title,
	})
}

func newOpenService(deps Dependencies) opener.Service {
	deps = withDependencyDefaults(deps)
	return opener.Service{
		Store:       deps.Store,
		LaunchShell: deps.LaunchShell,
	}
}

func openerIsDefaultError(err error) bool {
	return errors.Is(err, opener.ErrDefaultTenantNotConfigured) ||
		errors.Is(err, opener.ErrDefaultEnvironmentNotConfigured) ||
		errors.Is(err, internal.ErrNotInitialized)
}
