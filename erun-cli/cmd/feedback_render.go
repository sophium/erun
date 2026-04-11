package cmd

import (
	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
)

const (
	dryRunFlagUsage  = "Resolve and trace mutating actions without executing them."
	verboseFlagUsage = "Increase trace verbosity. -v logs command flow and side effects."
)

func addDryRunFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, dryRunFlagUsage)
}

func isDryRunCommand(cmd *cobra.Command) bool {
	dryRun, err := cmd.Flags().GetBool("dry-run")
	return err == nil && dryRun
}

func commandVerbosity(cmd *cobra.Command) int {
	verbosity, err := cmd.Flags().GetCount("verbose")
	if err != nil {
		return 0
	}
	return verbosity
}

func commandContext(cmd *cobra.Command) common.Context {
	loggerVerbosity := common.TraceLoggerVerbosity(commandVerbosity(cmd))
	if isDryRunCommand(cmd) && loggerVerbosity < 2 {
		loggerVerbosity = 2
	}
	return common.Context{
		Logger: common.NewLoggerWithWriters(loggerVerbosity, cmd.ErrOrStderr(), cmd.ErrOrStderr()),
		DryRun: isDryRunCommand(cmd),
		Stdin:  cmd.InOrStdin(),
		Stdout: cmd.OutOrStdout(),
		Stderr: cmd.ErrOrStderr(),
	}
}
