package eruncommon

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	VerbosityInfo  = 0
	VerbosityDebug = 1
	VerbosityTrace = 2
)

const (
	colorReset = "\033[0m"
	colorInfo  = "\033[1;34m"
	colorDebug = "\033[1;34m"
	colorTrace = "\033[36m"
	colorError = "\033[31m"
)

type Logger struct {
	verbosity int
	stdout    io.Writer
	stderr    io.Writer
	// traceSink, when set, receives every line the logger could print at
	// VerbosityTrace — independent of the terminal verbosity and always
	// uncolored — so the per-env debug-output log captures the full trace
	// even for plain invocations (issue #466).
	traceSink io.Writer
}

func NewLogger(verbosity int) Logger {
	return Logger{verbosity: clampVerbosity(verbosity)}
}

func NewLoggerWithWriters(verbosity int, stdout, stderr io.Writer) Logger {
	return Logger{
		verbosity: clampVerbosity(verbosity),
		stdout:    stdout,
		stderr:    stderr,
	}
}

func (l Logger) Verbosity() int { return l.verbosity }

// WithTraceSink returns a copy of the logger that mirrors every loggable
// line (at any level, including lines the terminal verbosity suppresses)
// into w. The sink is write-only telemetry: failures to write are ignored
// so a full disk or rotated file can never break the command.
func (l Logger) WithTraceSink(w io.Writer) Logger {
	l.traceSink = w
	return l
}

func (l Logger) sink(message string) {
	if l.traceSink == nil {
		return
	}
	_, _ = fmt.Fprintln(l.traceSink, message)
}

func clampVerbosity(verbosity int) int {
	if verbosity < 0 {
		return verbosity
	}
	if verbosity > VerbosityTrace {
		return VerbosityTrace
	}
	return verbosity
}

func (l Logger) Info(message string) {
	l.sink(message)
	if l.verbosity < VerbosityInfo {
		return
	}
	out := l.stdoutWriter()
	if l.verbosity >= VerbosityDebug {
		_, _ = fmt.Fprintln(out, maybeColorize(out, message, colorInfo))
		return
	}
	_, _ = fmt.Fprintln(out, message)
}

func (l Logger) Debug(message string) {
	l.sink(message)
	if l.verbosity < VerbosityDebug {
		return
	}
	out := l.stdoutWriter()
	_, _ = fmt.Fprintln(out, maybeColorize(out, message, colorDebug))
}

func (l Logger) Trace(message string) {
	l.sink(message)
	if l.verbosity < VerbosityTrace {
		return
	}
	out := l.stdoutWriter()
	_, _ = fmt.Fprintln(out, maybeColorize(out, message, colorTrace))
}

func (l Logger) Error(message string) {
	l.sink(message)
	_, _ = fmt.Fprintln(l.stderrWriter(), maybeColorize(l.stderrWriter(), message, colorError))
}

func (l *Logger) Fatal(err error) {
	if err != nil {
		_, _ = fmt.Fprintln(l.stderrWriter(), maybeColorize(l.stderrWriter(), err.Error(), colorError))
	}
}

func colorize(message, color string) string {
	return color + message + colorReset
}

func maybeColorize(out io.Writer, message, color string) string {
	if !shouldColorizeWriter(out) {
		return message
	}
	return colorize(message, color)
}

func (l Logger) stdoutWriter() io.Writer {
	if l.stdout != nil {
		return l.stdout
	}
	return os.Stdout
}

func (l Logger) stderrWriter() io.Writer {
	if l.stderr != nil {
		return l.stderr
	}
	return os.Stderr
}

func shouldColorizeWriter(out io.Writer) bool {
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

	info, err := file.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
