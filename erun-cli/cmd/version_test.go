package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBuildInfoDefaults(t *testing.T) {
	v, c, d := buildInfo()
	if v != "dev" || c != "" || d != "" {
		t.Fatalf("unexpected defaults: %s %s %s", v, c, d)
	}
}

func TestVersionCommandOutput(t *testing.T) {
	prevV, prevC, prevD := buildInfo()
	t.Cleanup(func() {
		setBuildInfo(prevV, prevC, prevD)
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

	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	setBuildInfo("1.2.3", "abcdef", "2024-01-01")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := buf.String(); got != "erun 1.2.3 (abcdef built 2024-01-01)\n" {
		t.Fatalf("unexpected output: %q", got)
	}

	buf.Reset()
	setBuildInfo("1.2.3", "", "")
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got := buf.String(); got != "erun 1.2.3\n" {
		t.Fatalf("unexpected tail-less output: %q", got)
	}
}

func TestVersionCommandPrefersVersionFileInCurrentDirectory(t *testing.T) {
	prevV, prevC, prevD := buildInfo()
	t.Cleanup(func() {
		setBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("9.9.9\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}

	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

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

	setBuildInfo("1.2.3", "abcdef", "2024-01-01")
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

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"version", "--dry-run", "-v"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute failed: %v", err)
	}

	if got := stdout.String(); got == "" {
		t.Fatalf("expected version output during dry-run, got %q", got)
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("erun version")) {
		t.Fatalf("expected dry-run trace, got %q", got)
	}
}
