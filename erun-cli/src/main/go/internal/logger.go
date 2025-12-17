package internal

import (
	"fmt"
	"os"
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
		fmt.Println(message)
	}
}

func (l Logger) Error(message string) {
	fmt.Fprintln(os.Stderr, message)
}

func (l *Logger) Fatal(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}
