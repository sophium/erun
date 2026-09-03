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
	if errors.Is(err, eruncommon.ErrPlatformAliasUnusable) {
		return platformAliasUnusableExitCode
	}
	return internal.ExitCodeFor(err)
}
