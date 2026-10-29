package jobs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestErunExecutableNear(t *testing.T) {
	for _, testCase := range []struct {
		name    string
		program string
		binary  string
	}{
		{name: "macos_app_bundle", program: "bin/ERun.app/Contents/MacOS/erun-app", binary: "bin/erun"},
		{name: "same_directory", program: "bin/emcp", binary: "bin/erun"},
		{name: "source_build_sibling_module", program: "erun-mcp/bin/emcp", binary: "erun-cli/bin/erun"},
		{name: "not_found", program: "bin/emcp"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			root := t.TempDir()
			program := writeTestExecutable(t, root, testCase.program)
			want := ""
			if testCase.binary != "" {
				want = writeTestExecutable(t, root, testCase.binary)
			}
			if got := erunExecutableNear(program, "erun"); got != want {
				t.Fatalf("erunExecutableNear(%q) = %q, want %q", program, got, want)
			}
		})
	}
}

func TestMacOSBundleContainerIgnoresNonBundleLayouts(t *testing.T) {
	for _, dir := range []string{
		filepath.FromSlash("/opt/erun/bin"),
		filepath.FromSlash("/opt/erun/MacOS"),
		filepath.FromSlash("/opt/erun/Contents/MacOS"),
		filepath.FromSlash("/opt/ERun.app/MacOS"),
	} {
		if got := macOSBundleContainer(dir); got != "" {
			t.Fatalf("macOSBundleContainer(%q) = %q, want no container", dir, got)
		}
	}
	bundled := filepath.FromSlash("/opt/erun/bin/ERun.app/Contents/MacOS")
	if got, want := macOSBundleContainer(bundled), filepath.FromSlash("/opt/erun/bin"); got != want {
		t.Fatalf("macOSBundleContainer(%q) = %q, want %q", bundled, got, want)
	}
}

func writeTestExecutable(t *testing.T, root, relative string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}
