package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/sophium/erun/cmd"
	common "github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/internal"
)

func TestRunInvokesCLI(t *testing.T) {
	called := false
	runCLI = func() error {
		called = true
		return nil
	}
	t.Cleanup(func() {
		runCLI = cmd.Execute
	})

	if exitCode := run(); exitCode != 0 {
		t.Fatalf("expected success exit code, got %d", exitCode)
	}

	if !called {
		t.Fatalf("expected CLI to run")
	}
}

func TestRunDoesNotLogReportedErrors(t *testing.T) {
	runCLI = func() error {
		return internal.MarkReported(common.ErrNotInGitRepository)
	}
	t.Cleanup(func() {
		runCLI = cmd.Execute
	})

	stderr := captureStderr(t, func() {
		if exitCode := run(); exitCode != 1 {
			t.Fatalf("expected failure exit code, got %d", exitCode)
		}
	})

	if stderr != "" {
		t.Fatalf("expected no additional stderr output, got %q", stderr)
	}
}

func TestRunLogsUnreportedErrors(t *testing.T) {
	expectedErr := errors.New("boom")
	runCLI = func() error {
		return expectedErr
	}
	t.Cleanup(func() {
		runCLI = cmd.Execute
	})

	stderr := captureStderr(t, func() {
		if exitCode := run(); exitCode != 1 {
			t.Fatalf("expected failure exit code, got %d", exitCode)
		}
	})

	if stderr == "" {
		t.Fatal("expected stderr output for unreported error")
	}
}

func captureStderr(t *testing.T, fn func()) string {
	t.Helper()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}

	original := os.Stderr
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = original
	})

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}
	os.Stderr = original

	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatalf("copy: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close reader: %v", err)
	}
	return buf.String()
}
