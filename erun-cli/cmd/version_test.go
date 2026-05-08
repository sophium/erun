package cmd

import (
	"bytes"
	"context"
	"os"
	"testing"

	common "github.com/sophium/erun/erun-common"
)

// The dry-run trace, --time, --no-registry default-skip, VERSION-file
// override, and audit/-v paths are covered by the integration suite
// (erun-integration/version_test.go). The cases below stay as unit tests
// because they verify presentation logic that depends on injected build
// metadata (ldflags) or on a synthesized RuntimeRegistryVersions response,
// neither of which is reachable from a dry-run scenario without stubbing.

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
