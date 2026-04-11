package eruncommon

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return capturePipe(t, &os.Stdout, fn)
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return capturePipe(t, &os.Stderr, fn)
}

func capturePipe(t *testing.T, target **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := *target
	*target = w

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	*target = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return buf.String()
}

func TestLoggerInfoAndDebug(t *testing.T) {
	logger := NewLogger(0)
	stdout := captureStdout(t, func() {
		logger.Info("info")
	})
	if strings.TrimSpace(stdout) != "info" {
		t.Fatalf("unexpected info output: %q", stdout)
	}

	quiet := NewLogger(-1)
	stdout = captureStdout(t, func() {
		quiet.Info("hidden")
	})
	if stdout != "" {
		t.Fatalf("expected no output, got %q", stdout)
	}

	verbose := NewLogger(1)
	stdout = captureStdout(t, func() {
		verbose.Debug("debug")
	})
	if strings.TrimSpace(stdout) != "debug" {
		t.Fatalf("unexpected debug output: %q", stdout)
	}

	stdout = captureStdout(t, func() {
		logger.Debug("muted")
	})
	if stdout != "" {
		t.Fatalf("expected muted debug output, got %q", stdout)
	}
}

func TestLoggerTrace(t *testing.T) {
	stdout := captureStdout(t, func() {
		NewLogger(2).Trace("trace")
	})
	expected := "trace\n"
	if stdout != expected {
		t.Fatalf("unexpected trace output: %q", stdout)
	}

	stdout = captureStdout(t, func() {
		NewLogger(1).Trace("hidden")
	})
	if stdout != "" {
		t.Fatalf("expected no trace output, got %q", stdout)
	}
}

func TestLoggerErrorAndFatal(t *testing.T) {
	logger := NewLogger(0)
	stderr := captureStderr(t, func() {
		logger.Error("boom")
	})
	expected := "boom\n"
	if stderr != expected {
		t.Fatalf("unexpected error output: %q", stderr)
	}

	stderr = captureStderr(t, func() {
		logger.Fatal(nil)
	})
	if stderr != "" {
		t.Fatalf("expected no fatal output for nil error, got %q", stderr)
	}

	stderr = captureStderr(t, func() {
		logger.Fatal(errors.New("fatal"))
	})
	expected = "fatal\n"
	if stderr != expected {
		t.Fatalf("unexpected fatal output: %q", stderr)
	}
}

func TestLoggerWithWriters(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer

	logger := NewLoggerWithWriters(2, &stdout, &stderr)
	logger.Info("info")
	logger.Debug("debug")
	logger.Trace("trace")
	logger.Error("boom")

	if got := stdout.String(); got != "info\ndebug\ntrace\n" {
		t.Fatalf("unexpected stdout output: %q", got)
	}
	if got := stderr.String(); got != "boom\n" {
		t.Fatalf("unexpected stderr output: %q", got)
	}
}

func TestColorize(t *testing.T) {
	if got := colorize("msg", colorTrace); got != colorTrace+"msg"+colorReset {
		t.Fatalf("unexpected colorized string: %q", got)
	}
}
