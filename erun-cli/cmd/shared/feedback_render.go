package shared

import (
	"fmt"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	dryRunFlagUsage  = "Resolve and trace mutating actions without executing them."
	timeFlagUsage    = "Print the elapsed runtime after the command finishes."
	VerboseFlagUsage = "Increase verbosity: -v streams external tool output, -vv adds erun command traces."
	outputFlagUsage  = "Result format: text (default, human stream) or json (structured result on stdout for orchestrators)."

	timingWrappedAnnotation = "erun.dev/timing-wrapped"
)

func AddDryRunFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, dryRunFlagUsage)
}

func AddTimeFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().Bool("time", false, timeFlagUsage)
}

func AddOutputFlag(cmd *cobra.Command) {
	cmd.PersistentFlags().String("output", "text", outputFlagUsage)
}

// commandOutputMode falls back to text on an unset or unparsable value.
func commandOutputMode(cmd *cobra.Command) model.OutputMode {
	raw, err := cmd.Flags().GetString("output")
	if err != nil {
		return model.OutputText
	}
	mode, err := core.ParseOutputMode(raw)
	if err != nil {
		return model.OutputText
	}
	return mode
}

// CommandWantsJSON treats --json as an alias of --output json.
func CommandWantsJSON(commandCtx core.Context, jsonFlag bool) bool {
	return jsonFlag || commandCtx.Output == model.OutputJSON
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
		verbosity = 0
	}
	userAskedForVerbose := err == nil && verbosity > 0
	if isExecCommand(cmd) && verbosity < core.VerbosityDebug {
		verbosity = core.VerbosityDebug
	}
	if isDryRunCommand(cmd) && verbosity < core.VerbosityTrace {
		verbosity = core.VerbosityTrace
	}
	if verbosity > core.VerbosityTrace {
		verbosity = core.VerbosityTrace
	}
	if shouldSilenceNoShellOutput(cmd, userAskedForVerbose) {
		return core.VerbosityInfo - 1
	}
	return verbosity
}

// shouldSilenceNoShellOutput keeps `eval "$(erun open ... --no-shell)"` silent on stderr.
func shouldSilenceNoShellOutput(cmd *cobra.Command, userAskedForVerbose bool) bool {
	return !userAskedForVerbose && !isDryRunCommand(cmd) && isNoShellCommand(cmd)
}

func isNoShellCommand(cmd *cobra.Command) bool {
	noShell, err := cmd.Flags().GetBool("no-shell")
	return err == nil && noShell
}

// commandScopesTenantByFlag reports whether cmd's --tenant scopes resolution, so it is offered as a fix only then.
func commandScopesTenantByFlag(cmd *cobra.Command) bool {
	if cmd.Flags().Lookup("tenant") == nil {
		return false
	}
	return !commandTenantFlagIsNotScoping(cmd)
}

func commandTenantFlagIsNotScoping(cmd *cobra.Command) bool {
	return cmd.Name() == "list"
}

func CommandContext(cmd *cobra.Command) core.Context {
	verbosity := commandVerbosity(cmd)
	return core.Context{
		Logger:                    core.NewLoggerWithWriters(verbosity, cmd.ErrOrStderr(), cmd.ErrOrStderr()),
		Verbosity:                 verbosity,
		DryRun:                    isDryRunCommand(cmd),
		Output:                    commandOutputMode(cmd),
		Command:                   cmd.CommandPath(),
		CommandScopesTenantByFlag: commandScopesTenantByFlag(cmd),
		Stdin:                     cmd.InOrStdin(),
		Stdout:                    cmd.OutOrStdout(),
		Stderr:                    cmd.ErrOrStderr(),
	}
}

func AuditCommand(cmd *cobra.Command, args []string) {
	ctx := CommandContext(cmd)
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
		if name, _, ok := strings.Cut(arg, "="); ok && isSensitiveFlag(name) {
			redacted = append(redacted, name+"=<redacted>")
			continue
		}
		redacted = append(redacted, arg)
		if isSensitiveFlag(arg) {
			redactNext = true
		}
	}
	return redacted
}

// Only flags can carry secrets; a bare word like a `token` command name is never redacted.
func isSensitiveFlag(value string) bool {
	if !strings.HasPrefix(value, "-") {
		return false
	}
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

func WrapCommandTreeWithElapsedTime(cmd *cobra.Command) {
	if cmd == nil || commandTimingWrapped(cmd) {
		return
	}

	markCommandTimingWrapped(cmd)
	wrapCommandWithElapsedTime(cmd)
	for _, child := range cmd.Commands() {
		WrapCommandTreeWithElapsedTime(child)
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
