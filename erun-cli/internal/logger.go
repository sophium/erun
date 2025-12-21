package internal

import (
	"fmt"
	"os"
)

const (
	colorReset = "\033[0m"
	colorTrace = "\033[36m"
	colorError = "\033[31m"
)

type Logger struct {
	verbosity int
}

func NewLogger(verbosity int) Logger {
	return Logger{verbosity: verbosity}
}

func (l Logger) Info(message string) {
	if l.verbosity >= 0 {
		fmt.Println(message)
	}
}

func (l Logger) Debug(message string) {
	if l.verbosity >= 1 {
		fmt.Println(message)
	}
}

func (l Logger) Trace(message string) {
	if l.verbosity >= 2 {
		fmt.Println(colorize(message, colorTrace))
	}
}

func (l Logger) Error(message string) {
	fmt.Fprintln(os.Stderr, colorize(message, colorError))
}

func (l *Logger) Fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, colorize(err.Error(), colorError))
	}
}

func colorize(message, color string) string {
	return color + message + colorReset
}
