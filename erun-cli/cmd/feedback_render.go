package cmd

import (
	"fmt"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	dryRunFlagUsage  = "Resolve and trace mutating actions without executing them."
	timeFlagUsage    = "Print the elapsed runtime after the command finishes."
	verboseFlagUsage = "Increase trace verbosity. -v logs command flow and side effects."

	timingWrappedAnnotation = "erun.dev/timing-wrapped"
)

func addDryRunFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, dryRunFlagUsage)
}

func addTimeFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("time", false, timeFlagUsage)
}

func isDryRunCommand(cmd *cobra.Command) bool {
	dryRun, err := cmd.Flags().GetBool("dry-run")
	return err == nil && dryRun
}

func shouldPrintElapsedTime(cmd *cobra.Command) bool {
	printTime, err := cmd.Flags().GetBool("time")
	return err == nil && printTime
}

func commandVerbosity(cmd *cobra.Command) int {
	verbosity, err := cmd.Flags().GetCount("verbose")
	if err != nil {
		return 0
	}
	if verbosity == 0 && isExecCommand(cmd) {
		return 1
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

func auditCommand(cmd *cobra.Command, args []string) {
	ctx := commandContext(cmd)
	ctx.Trace("audit: " + formatAuditCommand(cmd, args))
}

func formatAuditCommand(cmd *cobra.Command, args []string) string {
	parts := strings.Fields(cmd.CommandPath())
	if len(parts) == 0 {
		parts = []string{"erun"}
	}
	parts = append(parts, changedFlagArgs(cmd)...)
	parts = append(parts, args...)
	return strings.Join(redactAuditArgs(parts), " ")
}

func changedFlagArgs(cmd *cobra.Command) []string {
	args := make([]string, 0)
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if flag.Name == "help" || flag.Name == "verbose" {
			return
		}
		name := "--" + flag.Name
		if flag.Value.Type() == "bool" {
			args = append(args, name)
			return
		}
		args = append(args, name, flag.Value.String())
	})
	return args
}

func redactAuditArgs(args []string) []string {
	redacted := make([]string, 0, len(args))
	redactNext := false
	for _, arg := range args {
		if redactNext {
			redacted = append(redacted, "<redacted>")
			redactNext = false
			continue
		}
		if name, _, ok := strings.Cut(arg, "="); ok && isSensitiveName(name) {
			redacted = append(redacted, name+"=<redacted>")
			continue
		}
		redacted = append(redacted, arg)
		if isSensitiveName(arg) {
			redactNext = true
		}
	}
	return redacted
}

func isSensitiveName(value string) bool {
	normalized := strings.ToLower(strings.TrimLeft(value, "-"))
	for _, token := range []string{"password", "passwd", "secret", "token", "apikey", "api-key", "access-key", "private-key"} {
		if strings.Contains(normalized, token) {
			return true
		}
	}
	return false
}

func isExecCommand(cmd *cobra.Command) bool {
	for current := cmd; current != nil; current = current.Parent() {
		if current.Name() == "exec" {
			return true
		}
	}
	return false
}

func wrapCommandTreeWithElapsedTime(cmd *cobra.Command) {
	if cmd == nil || commandTimingWrapped(cmd) {
		return
	}

	markCommandTimingWrapped(cmd)
	wrapCommandWithElapsedTime(cmd)
	for _, child := range cmd.Commands() {
		wrapCommandTreeWithElapsedTime(child)
	}
}

func wrapCommandWithElapsedTime(cmd *cobra.Command) {
	if cmd.RunE != nil {
		run := cmd.RunE
		cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
			if !shouldPrintElapsedTime(cmd) {
				return run(cmd, args)
			}

			started := time.Now()
			defer func() {
				printElapsedTime(cmd, time.Since(started))
			}()
			return run(cmd, args)
		}
		return
	}

	if cmd.Run == nil {
		return
	}

	run := cmd.Run
	cmd.Run = func(cmd *cobra.Command, args []string) {
		if !shouldPrintElapsedTime(cmd) {
			run(cmd, args)
			return
		}

		started := time.Now()
		defer func() {
			printElapsedTime(cmd, time.Since(started))
		}()
		run(cmd, args)
	}
}

func printElapsedTime(cmd *cobra.Command, elapsed time.Duration) {
	rounded := elapsed.Round(time.Millisecond)
	if elapsed > 0 && rounded == 0 {
		rounded = time.Millisecond
	}
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "elapsed: %s\n", rounded)
}

func commandTimingWrapped(cmd *cobra.Command) bool {
	return cmd.Annotations != nil && cmd.Annotations[timingWrappedAnnotation] == "true"
}

func markCommandTimingWrapped(cmd *cobra.Command) {
	if cmd.Annotations == nil {
		cmd.Annotations = make(map[string]string)
	}
	cmd.Annotations[timingWrappedAnnotation] = "true"
}
