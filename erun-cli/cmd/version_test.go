package cmd

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	common "github.com/sophium/erun/erun-common"
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
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	setBuildInfo("1.2.3", "abcdef", "2024-01-01")
	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := buf.String(); got != "erun 1.2.3 (abcdef built 2024-01-01)\n" {
		t.Fatalf("unexpected output: %q", got)
	}

	buf.Reset()
	setBuildInfo("1.2.3", "", "")
	requireNoError(t, cmd.Execute(), "Execute failed")
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
	requireNoError(t, os.WriteFile(filepath.Join(workdir, "VERSION"), []byte("9.9.9\n"), 0o644), "write VERSION")

	cmd := newTestRootCmd(testRootDeps{})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	setBuildInfo("1.2.3", "abcdef", "2024-01-01")
	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := buf.String(); got != "erun 9.9.9 (abcdef built 2024-01-01)\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestVersionCommandPrintsRegistryVersions(t *testing.T) {
	prevV, prevC, prevD := buildInfo()
	t.Cleanup(func() {
		setBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := newTestRootCmd(testRootDeps{
		ResolveRuntimeRegistryVersions: func(context.Context) (common.RuntimeRegistryVersions, error) {
			return common.RuntimeRegistryVersions{
				Image:          "erunpaas/erun-devops",
				LatestStable:   "1.0.50",
				LatestSnapshot: "1.0.51-snapshot-20260424100000",
			}, nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version"})

	setBuildInfo("1.2.3", "abcdef", "2024-01-01")
	requireNoError(t, cmd.Execute(), "Execute failed")

	want := "erun 1.2.3 (abcdef built 2024-01-01)\n" +
		"latest stable: 1.0.50\n" +
		"latest snapshot: 1.0.51-snapshot-20260424100000\n"
	if got := buf.String(); got != want {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestVersionCommandCanSkipRegistryVersions(t *testing.T) {
	prevV, prevC, prevD := buildInfo()
	t.Cleanup(func() {
		setBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	called := false
	cmd := newTestRootCmd(testRootDeps{
		ResolveRuntimeRegistryVersions: func(context.Context) (common.RuntimeRegistryVersions, error) {
			called = true
			return common.RuntimeRegistryVersions{LatestStable: "1.0.50"}, nil
		},
	})
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs([]string{"version", "--no-registry"})

	setBuildInfo("1.2.3", "", "")
	requireNoError(t, cmd.Execute(), "Execute failed")

	if called {
		t.Fatal("registry resolver was called")
	}
	if got := buf.String(); got != "erun 1.2.3\n" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestVersionCommandDryRunPrintsTraceWithoutOutput(t *testing.T) {
	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"version", "--dry-run", "-v"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := stdout.String(); got == "" {
		t.Fatalf("expected version output during dry-run, got %q", got)
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("erun version")) {
		t.Fatalf("expected dry-run trace, got %q", got)
	}
}

func TestVersionCommandTimeFlagPrintsElapsedTimeToStderr(t *testing.T) {
	prevV, prevC, prevD := buildInfo()
	t.Cleanup(func() {
		setBuildInfo(prevV, prevC, prevD)
	})

	workdir := t.TempDir()
	prevWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	requireNoError(t, os.Chdir(workdir), "chdir")
	t.Cleanup(func() {
		_ = os.Chdir(prevWD)
	})

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"version", "--time"})

	setBuildInfo("1.2.3", "", "")
	requireNoError(t, cmd.Execute(), "Execute failed")

	if got := stdout.String(); got != "erun 1.2.3\n" {
		t.Fatalf("unexpected stdout: %q", got)
	}
	if got := stderr.String(); got == "" || !bytes.Contains([]byte(got), []byte("elapsed:")) {
		t.Fatalf("expected elapsed time on stderr, got %q", got)
	}
}
