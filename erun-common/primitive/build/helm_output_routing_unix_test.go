//go:build unix

package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

// Helm's progress line on stdout would break `erun build --output json` parsing.
func TestHelmProgressStaysOffStdout(t *testing.T) {
	binDir := t.TempDir()
	script := "#!/bin/sh\necho \"Successfully packaged chart and saved it to: x.tgz\"\necho \"helm stderr\" >&2\n"
	if err := os.WriteFile(filepath.Join(binDir, "helm"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake helm: %v", err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	for name, run := range map[string]func(core.Context, helm.CommandSpec) error{
		"runHelmCommand": helm.RunHelmCommand,
		"runHelmCommandCapturingOutput": func(ctx core.Context, spec helm.CommandSpec) error {
			out, err := helm.RunHelmCommandCapturingOutput(ctx, spec)
			if !strings.Contains(out, "Successfully packaged chart") {
				t.Errorf("captured output lost helm's stdout: %q", out)
			}
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			// helm's two streams are copied concurrently onto one writer.
			var stdout syncBuffer
			var stderr syncBuffer
			ctx := core.Context{Stdout: &stdout, Stderr: &stderr}
			if err := run(ctx, helm.CommandSpec{Name: "helm", Args: []string{"package", "chart"}}); err != nil {
				t.Fatalf("helm: %v", err)
			}
			if out := stdout.snapshot(); out != "" {
				t.Fatalf("helm progress reached stdout: %q", out)
			}
			if errOut := stderr.snapshot(); !strings.Contains(errOut, "Successfully packaged chart") || !strings.Contains(errOut, "helm stderr") {
				t.Fatalf("helm output missing from stderr: %q", errOut)
			}
		})
	}
}
