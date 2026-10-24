package shared

import (
	"strings"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

func AddCommands(parent *cobra.Command, commands ...*cobra.Command) {
	for _, command := range commands {
		if command == nil {
			continue
		}
		WrapCommandTreeWithElapsedTime(command)
		parent.AddCommand(command)
	}
}

func NewCommandGroup(use, short string, commands ...*cobra.Command) *cobra.Command {
	command := &cobra.Command{
		Use:           use,
		Short:         short,
		SilenceErrors: true,
		SilenceUsage:  true,
	}
	AddCommands(command, commands...)
	return command
}

// ScopedOpenParams adapts the --tenant/--environment flag pair every
// environment-scoped command shares: an omitted flag means "the current scope",
// resolved the same way as for `deploy` or `open`.
// cmd supplies the command's own invocation (CommandPath), so a
// tenant-resolution failure names the command the operator actually ran and
// offers that command's own recovery. Every scoped command routing through
// here is what keeps that true when a new command adopts the flag pair.
func ScopedOpenParams(command, tenant, environment string) common.OpenParams {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	return common.OpenParams{
		Tenant:                    tenant,
		Environment:               environment,
		UseDefaultTenant:          tenant == "",
		UseDefaultEnvironment:     environment == "",
		Command:                   command,
		CommandScopesTenantByFlag: true,
	}
}
