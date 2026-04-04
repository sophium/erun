package eruncommon

import (
	"fmt"
	"io"
	"os"
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

func (l Logger) Info(message string) {
	if l.verbosity >= 0 {
		fmt.Fprintln(l.stdoutWriter(), message)
	}
}

func (l Logger) Debug(message string) {
	if l.verbosity >= 1 {
		fmt.Fprintln(l.stdoutWriter(), message)
	}
}

func (l Logger) Trace(message string) {
	if l.verbosity >= 2 {
		fmt.Fprintln(l.stdoutWriter(), colorize(message, colorTrace))
	}
}

func (l Logger) Error(message string) {
	fmt.Fprintln(l.stderrWriter(), colorize(message, colorError))
}

func (l *Logger) Fatal(err error) {
	if err != nil {
		fmt.Fprintln(l.stderrWriter(), colorize(err.Error(), colorError))
	}
}

func colorize(message, color string) string {
	return color + message + colorReset
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
