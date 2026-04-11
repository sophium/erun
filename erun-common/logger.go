package eruncommon

import (
	"fmt"
	"io"
	"os"
	"strings"
)

const (
	colorReset = "\033[0m"
	colorTrace = "\033[36m"
	colorError = "\033[31m"
)

type Logger struct {
	verbosity int
	stdout    io.Writer
	stderr    io.Writer
}

func NewLogger(verbosity int) Logger {
	return Logger{verbosity: verbosity}
}

func NewLoggerWithWriters(verbosity int, stdout, stderr io.Writer) Logger {
	return Logger{
		verbosity: verbosity,
		stdout:    stdout,
		stderr:    stderr,
	}
}

func TraceLoggerVerbosity(verbosity int) int {
	if verbosity <= 0 {
		return 0
	}
	return verbosity + 1
}

func (l Logger) Info(message string) {
	if l.verbosity >= 0 {
		_, _ = fmt.Fprintln(l.stdoutWriter(), message)
	}
}

func (l Logger) Debug(message string) {
	if l.verbosity >= 1 {
		_, _ = fmt.Fprintln(l.stdoutWriter(), message)
	}
}

func (l Logger) Trace(message string) {
	if l.verbosity >= 2 {
		_, _ = fmt.Fprintln(l.stdoutWriter(), maybeColorize(l.stdoutWriter(), message, colorTrace))
	}
}

func (l Logger) Error(message string) {
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
