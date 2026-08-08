package eruncommon

import (
	"os"
	"path/filepath"
	"testing"
)

// The shipped desktop runs from inside a macOS .app bundle, so the erun it needs
// sits three levels above its own directory rather than beside it. Missing that
// layout is what left an orchestrator session with none of its environment
// tools, so every layout the resolver must handle is pinned here.
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

// A bundle-shaped path is only recognized as one when every level matches, so an
// ordinary directory named MacOS cannot pull the search outside its own tree.
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
