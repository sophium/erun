package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type CommandTrace struct {
	Dir  string
	Name string
	Args []string
}

const (
	dryRunFlagUsage  = "Resolve and print the planned actions without executing them."
	verboseFlagUsage = "Increase logging verbosity. -v prints resolved command plans, -vv adds decision notes, -vvv includes internal trace logs."

	traceColorReset    = "\033[0m"
	traceCommandColor  = "\033[32m"
	traceDecisionColor = "\033[33m"
)

type traceLineKind int

const (
	traceLineKindCommand traceLineKind = iota
	traceLineKindDecision
)

func (t CommandTrace) String() string {
	parts := make([]string, 0, len(t.Args)+1)
	if strings.TrimSpace(t.Name) != "" {
		parts = append(parts, shellQuote(t.Name))
	}
	for _, arg := range t.Args {
		parts = append(parts, shellQuote(arg))
	}
	return strings.Join(parts, " ")
}

func addDryRunFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("dry-run", false, dryRunFlagUsage)
}

func isDryRunCommand(cmd *cobra.Command) bool {
	dryRun, err := cmd.Flags().GetBool("dry-run")
	return err == nil && dryRun
}

func commandTraceEnabled(cmd *cobra.Command) bool {
	if isDryRunCommand(cmd) {
		return true
	}
	return commandVerbosity(cmd) > 0
}

func decisionTraceEnabled(cmd *cobra.Command) bool {
	verbosity := commandVerbosity(cmd)
	if isDryRunCommand(cmd) {
		return verbosity > 0
	}
	return verbosity > 1
}

func internalTraceVerbosity(cmd *cobra.Command) int {
	verbosity := commandVerbosity(cmd)
	if verbosity <= 0 {
		return 0
	}
	if isDryRunCommand(cmd) {
		return verbosity
	}
	return max(0, verbosity-1)
}

func commandVerbosity(cmd *cobra.Command) int {
	verbosity, err := cmd.Flags().GetCount("verbose")
	if err != nil {
		return 0
	}
	return verbosity
}

func tracePrefix(cmd *cobra.Command) string {
	if isDryRunCommand(cmd) {
		return "[dry-run]"
	}
	return "[trace]"
}

func emitTraceNotes(cmd *cobra.Command, out io.Writer, notes ...string) {
	if !decisionTraceEnabled(cmd) {
		return
	}

	prefix := tracePrefix(cmd)
	for _, note := range notes {
		note = strings.TrimSpace(note)
		if note == "" {
			continue
		}
		writeTraceLine(out, traceLine(out, traceLineKindDecision, fmt.Sprintf("%s %s", prefix, note)))
	}
}

func emitCommandTrace(cmd *cobra.Command, out io.Writer, trace CommandTrace, notes ...string) {
	if !commandTraceEnabled(cmd) {
		return
	}

	prefix := tracePrefix(cmd)

	if strings.TrimSpace(trace.Dir) != "" {
		writeTraceLine(out, traceLine(out, traceLineKindCommand, fmt.Sprintf("%s cwd=%s", prefix, shellQuote(trace.Dir))))
	}
	writeTraceLine(out, traceLine(out, traceLineKindCommand, fmt.Sprintf("%s %s", prefix, trace.String())))
	emitTraceNotes(cmd, out, notes...)
}

func emitTraceBlock(cmd *cobra.Command, out io.Writer, label, body string) {
	if !commandTraceEnabled(cmd) {
		return
	}

	label = strings.TrimSpace(label)
	body = strings.TrimRight(body, "\n")
	if label == "" || body == "" {
		return
	}

	prefix := tracePrefix(cmd)
	writeTraceLine(out, traceLine(out, traceLineKindCommand, fmt.Sprintf("%s %s:", prefix, label)))
	for _, line := range strings.Split(body, "\n") {
		writeTraceLine(out, traceLine(out, traceLineKindCommand, fmt.Sprintf("%s   %s", prefix, line)))
	}
}

func writeTraceLine(out io.Writer, message string) {
	_, _ = fmt.Fprintln(out, message)
}

func traceLine(out io.Writer, kind traceLineKind, message string) string {
	if !shouldColorTraceOutput(out) {
		return message
	}
	return traceColor(kind) + message + traceColorReset
}

func traceColor(kind traceLineKind) string {
	switch kind {
	case traceLineKindDecision:
		return traceDecisionColor
	default:
		return traceCommandColor
	}
}

func shouldColorTraceOutput(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	if strings.EqualFold(os.Getenv("TERM"), "dumb") {
		return false
	}

	file, ok := out.(*os.File)
	if !ok {
		return false
	}

	return term.IsTerminal(int(file.Fd()))
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if isShellSafe(value) {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func isShellSafe(value string) bool {
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case strings.ContainsRune("/._:=+-", r):
		default:
			return false
		}
	}
	return true
}
