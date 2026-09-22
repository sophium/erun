package main

import (
	"errors"
	"os"

	"github.com/sophium/erun/cmd"
	eruncommon "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

// platformAliasUnusableExitCode marks a command that could not even resolve
// a usable erun platform alias (none configured, an incomplete one, the
// wrong alias type) -- distinct from an ordinary failure so a caller wiring
// one of these commands into a script, and checking only the exit code, does
// not mistake "this environment cannot record anything on the platform" for
// "it tried and failed" or worse, silently believe it succeeded. Continues
// the sequence job.go's jobAwaitTimeoutExitCode (124) and
// jobAwaitUnknownExitCode (125) and mcp_call.go's
// mcpChannelUnreachableExitCode (126) already established.
const platformAliasUnusableExitCode = 127

func main() {
	if exitCode := run(); exitCode != 0 {
		os.Exit(exitCode)
	}
}

func run() int {
	err := cmd.Execute()
	if err == nil {
		return 0
	}
	if !internal.IsReported(err) {
		logger := eruncommon.NewLogger(0)
		logger.Fatal(err)
	}
	return exitCodeFor(err)
}

// exitCodeFor resolves the process exit code for an already-reported failure.
// The platform-alias condition keeps its own code; everything else falls through
// to the code the failure itself carries, defaulting to 1. A tenant-selection
// refusal is tagged with its own code by the command that produced it, so it can
// never be read as either of the other two.
func exitCodeFor(err error) int {
	if errors.Is(err, eruncommon.ErrPlatformAliasUnusable) {
		return platformAliasUnusableExitCode
	}
	return internal.ExitCodeFor(err)
}
