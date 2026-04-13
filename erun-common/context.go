package eruncommon

import (
	"fmt"
	"io"
	"strings"
)

type Context struct {
	Logger Logger
	DryRun bool
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

func (c Context) Trace(message string) {
	c.Logger.Trace(message)
}

func (c Context) Info(message string) {
	c.Logger.Info(message)
}

func (c Context) TraceCommand(dir, name string, args ...string) {
	c.Logger.Trace(formatShellCommand(dir, name, args...))
}

func (c Context) TraceBlock(label, body string) {
	label = strings.TrimSpace(label)
	body = strings.TrimRight(body, "\n")
	if label == "" || body == "" {
		return
	}

	c.Logger.Trace(label + ":")
	for _, line := range strings.Split(body, "\n") {
		c.Logger.Trace("  " + line)
	}
}

func formatShellCommand(dir, name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	if strings.TrimSpace(name) != "" {
		parts = append(parts, traceShellQuote(name))
	}
	for _, arg := range args {
		parts = append(parts, traceShellQuote(arg))
	}

	command := strings.Join(parts, " ")
	if strings.TrimSpace(dir) == "" {
		return command
	}
	return fmt.Sprintf("cd %s && %s", traceShellQuote(dir), command)
}

func traceShellQuote(value string) string {
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
