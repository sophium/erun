package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

// runningCommandSeed is the per-command metadata each command resolves at
// the time its RunE is about to fire. Fields are best-effort: empty strings
// are fine, the desktop drawer renders a sensible card from whatever the
// command knew at start. Deploy fills in extra fields from inside
// RunHelmDeploy so commands that don't know release/namespace yet (build,
// init) can leave those empty.
type runningCommandSeed struct {
	command     string
	tenant      string
	environment string
	version     string
	component   string
	summary     string
}

// withRunningCommandMarker wraps a cobra RunE so the desktop's running-
// command queue picks up every invocation, regardless of who launched
// the binary. Dry-run does not write a marker (RegisterRunningCommand
// short-circuits), so integration scenarios are unaffected.
//
// The seedFn is called inside the cobra wrapper so it can read flag
// values populated by cobra before RunE runs but after parsing.
func withRunningCommandMarker(seedFn func(*cobra.Command, []string) runningCommandSeed, run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		seed := seedFn(cmd, args)
		if seed.command == "" {
			return run(cmd, args)
		}
		ctx := commandContext(cmd)
		handle, regErr := common.RegisterRunningCommand(ctx, common.RunningCommand{
			Command:     seed.command,
			Tenant:      seed.tenant,
			Environment: seed.environment,
			Version:     seed.version,
			Component:   seed.component,
			Summary:     seed.summary,
		})
		if regErr != nil {
			ctx.Trace("running-command: register failed (" + regErr.Error() + ")")
		}
		defer handle.Release()
		return run(cmd, args)
	}
}
