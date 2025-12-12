package cmd_test

import (
	"bytes"
	"strings"
	"testing"

	cmdpkg "github.com/sophium/erun/cmd"
)

func TestVersionCommandOutputsMetadata(t *testing.T) {
	cmd := cmdpkg.NewVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	prevV, prevC, prevD := cmdpkg.BuildInfo()
	t.Cleanup(func() {
		cmdpkg.SetBuildInfo(prevV, prevC, prevD)
	})

	cmdpkg.SetBuildInfo("v1.2.3", "abcdef1", "2024-01-02T03:04:05Z")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	want := "erun v1.2.3 (abcdef1 built 2024-01-02T03:04:05Z)"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestVersionCommandWithoutMetadata(t *testing.T) {
	cmd := cmdpkg.NewVersionCmd()
	buf := new(bytes.Buffer)
	cmd.SetOut(buf)
	cmd.SetErr(buf)

	prevV, prevC, prevD := cmdpkg.BuildInfo()
	t.Cleanup(func() {
		cmdpkg.SetBuildInfo(prevV, prevC, prevD)
	})

	cmdpkg.SetBuildInfo("dev", "", "")

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command returned error: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if got != "erun dev" {
		t.Fatalf("expected %q, got %q", "erun dev", got)
	}
}
