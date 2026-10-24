package shared

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
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

// ScopedOpenParams maps --tenant/--environment to OpenParams; empty means the current scope.
// command names the invocation so resolution failures cite it.
func ScopedOpenParams(command, tenant, environment string) model.OpenParams {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	return model.OpenParams{
		Tenant:                    tenant,
		Environment:               environment,
		UseDefaultTenant:          tenant == "",
		UseDefaultEnvironment:     environment == "",
		Command:                   command,
		CommandScopesTenantByFlag: true,
	}
}
