package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sophium/erun/cmd"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/cloud"
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

// Seams for the argv[0] dispatch test.
var (
	execClaude             = podruntime.RunClaude
	executeERun            = cmd.Execute
	claudeStderr io.Writer = os.Stderr
)

func main() {
	if exitCode := run(os.Args); exitCode != 0 {
		os.Exit(exitCode)
	}
}

// run dispatches on the name erun was started as: the runtime image links
// claude to erun so a bare `claude` resumes the directory's conversation, and
// that path stays clear of the erun command tree.
func run(argv []string) int {
	if len(argv) > 0 && podruntime.IsClaudeInvocation(argv[0]) {
		return runClaude(argv[1:])
	}
	return runERun()
}

// runClaude returns only when the hand-over to Claude Code failed.
func runClaude(args []string) int {
	if err := execClaude(args); err != nil {
		_, _ = fmt.Fprintln(claudeStderr, err)
		return 1
	}
	return 0
}

func runERun() int {
	err := executeERun()
	if err == nil {
		return 0
	}
	if !internal.IsReported(err) {
		logger := core.NewLogger(0)
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
	if errors.Is(err, cloud.ErrPlatformAliasUnusable) {
		return platformAliasUnusableExitCode
	}
	return internal.ExitCodeFor(err)
}
