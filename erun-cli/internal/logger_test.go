package internal

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
	expected := colorize("trace", colorTrace) + "\n"
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
	expected := colorize("boom", colorError) + "\n"
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
	expected = colorize("fatal", colorError) + "\n"
	if stderr != expected {
		t.Fatalf("unexpected fatal output: %q", stderr)
	}
}

func TestColorize(t *testing.T) {
	if got := colorize("msg", colorTrace); got != colorTrace+"msg"+colorReset {
		t.Fatalf("unexpected colorized string: %q", got)
	}
}
