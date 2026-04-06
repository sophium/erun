package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildInfoDefaults(t *testing.T) {
	v, c, d := BuildInfo()
	if v != "dev" || c != "" || d != "" {
		t.Fatalf("unexpected defaults: %s %s %s", v, c, d)
	}
}

func TestVersionCommandOutput(t *testing.T) {
	prevV, prevC, prevD := BuildInfo()
	t.Cleanup(func() {
		SetBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := NewVersionCmd(Dependencies{}, nil)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	SetBuildInfo("1.2.3", "abcdef", "2024-01-01")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := buf.String(); got != "erun 1.2.3 (abcdef built 2024-01-01)\n" {
		t.Fatalf("unexpected output: %q", got)
	}

	buf.Reset()
	SetBuildInfo("1.2.3", "", "")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got := buf.String(); got != "erun 1.2.3\n" {
		t.Fatalf("unexpected tail-less output: %q", got)
	}
}

func TestVersionCommandPrefersVersionFileInCurrentDirectory(t *testing.T) {
	prevV, prevC, prevD := BuildInfo()
	t.Cleanup(func() {
		SetBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	cmd := NewVersionCmd(Dependencies{}, nil)
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	SetBuildInfo("1.2.3", "abcdef", "2024-01-01")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := buf.String(); got != "erun 9.9.9 (abcdef built 2024-01-01)\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestVersionCommandDryRunPrintsTraceWithoutOutput(t *testing.T) {
	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(workdir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := NewVersionCmd(Dependencies{}, nil)
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"--dry-run"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got != "" {
		t.Fatalf("expected no version output during dry-run, got %q", got)
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("[dry-run] erun version")) {
		t.Fatalf("expected dry-run trace, got %q", got)
	}
}
