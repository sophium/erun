package cmd

import (
	"time"

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
// command queue picks up every invocation, regardless of who launched the
// binary. Dry-run does not write a marker (RegisterRunningCommand
// short-circuits), so integration scenarios are unaffected.
//
// On exit, the wrapper calls FinalizeRunningCommand with the run's
// terminal status — "failed" + error reason when run returned an error,
// "succeeded" otherwise. That populates the marker's Status field briefly
// before deletion so the desktop watcher can render the failure cause
// (this matters for `erun open` which can transitively invoke deploy:
// when that deploy fails the user must see why).
func withRunningCommandMarker(seedFn func(*cobra.Command, []string) runningCommandSeed, run func(*cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) (err error) {
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
		defer func() {
			status := "succeeded"
			errMsg := ""
			if err != nil {
				status = "failed"
				errMsg = err.Error()
			}
			common.FinalizeRunningCommand(handle, status, errMsg, time.Time{}, common.FinalizeRunningCommandPollWindow)
		}()
		return run(cmd, args)
	}
}
