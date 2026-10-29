package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/sophium/erun/cmd"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-common/infra/cloud/providerconfig"
	"github.com/sophium/erun/internal"
)

// platformAliasUnusableExitCode marks a command that cannot resolve a usable platform
// alias, so scripts don't read it as an ordinary failure or a success.
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

// run dispatches on argv[0]: the runtime image links claude to erun.
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

// exitCodeFor resolves the exit code for an already-reported failure, defaulting to 1.
func exitCodeFor(err error) int {
	if errors.Is(err, providerconfig.ErrPlatformAliasUnusable) {
		return platformAliasUnusableExitCode
	}
	return internal.ExitCodeFor(err)
}
