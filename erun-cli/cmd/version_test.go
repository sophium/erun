package cmd

import (
	"bytes"
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

	cmd := NewVersionCmd()
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
