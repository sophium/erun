package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// TestManageDialogRepoPathMatchesInit pins the property that the dialog and init
// agree on a repository path: any path the Manage dialog accepts, init accepts,
// and any path init rejects, the dialog rejects with a comparable message.
//
// It drives the dialog's save path (updatedEnvironmentConfig, the function
// SaveEnvironmentConfig persists through) over the fixture set declared in
// erun-common/local_repo_path_test.go and asserts the dialog's verdict equals
// the shared definition's verdict for every one of them — so the two sides are
// compared to each other, not to a second copy of the expectations.
func TestManageDialogRepoPathMatchesInit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}

	fixtures := []struct {
		name string
		path string
	}{
		{name: "existing absolute directory", path: dir},
		{name: "existing directory with trailing separator", path: dir + string(os.PathSeparator)},
		{name: "padded but real directory", path: "  " + dir + "  "},
		{name: "empty", path: ""},
		{name: "blank", path: "   "},
		{name: "relative", path: filepath.Join("relative", "repo")},
		{name: "dot-relative", path: "." + string(os.PathSeparator) + "repo"},
		{name: "missing absolute", path: filepath.Join(dir, "does-not-exist-2383")},
		{name: "absolute path to a file", path: file},
	}

	for _, envType := range []eruncommon.EnvironmentType{
		eruncommon.EnvironmentTypeLocalAgent,
		eruncommon.EnvironmentTypeHost,
	} {
		for _, tc := range fixtures {
			app := &App{}
			existing := eruncommon.EnvConfig{Name: "dev", Type: envType}
			config := uiEnvironmentConfig{
				Name:          "dev",
				Type:          envType,
				LocalRepoPath: tc.path,
			}
			updated, err := app.updatedEnvironmentConfig(config, existing)
			initErr := eruncommon.ValidateHostRepoPath(envType, tc.path)

			switch {
			case (err == nil) != (initErr == nil):
				t.Errorf("%s / %s: dialog err=%v but init err=%v for %q — the two sides disagree",
					envType, tc.name, err, initErr, tc.path)
			case err != nil && !strings.Contains(err.Error(), eruncommon.HostRepoPathRequirement(envType)):
				t.Errorf("%s / %s: dialog refusal %q does not carry init's reason %q",
					envType, tc.name, err, eruncommon.HostRepoPathRequirement(envType))
			case err == nil && strings.TrimSpace(updated.LocalRepoPath) != strings.TrimSpace(tc.path):
				t.Errorf("%s / %s: dialog accepted %q but saved %q",
					envType, tc.name, tc.path, updated.LocalRepoPath)
			}
		}
	}
}

// TestManageDialogRepoPathIgnoresOtherTypes pins the other half: a remote or
// runtime env's recorded path names an in-pod directory, so the dialog must keep
// saving it unchecked rather than start refusing deploys' worth of edits.
func TestManageDialogRepoPathIgnoresOtherTypes(t *testing.T) {
	for _, envType := range []eruncommon.EnvironmentType{
		eruncommon.EnvironmentTypeRemoteAgent,
		eruncommon.EnvironmentTypeRuntime,
	} {
		app := &App{}
		existing := eruncommon.EnvConfig{Name: "dev", Type: envType}
		config := uiEnvironmentConfig{Name: "dev", Type: envType, LocalRepoPath: "/in-pod/worktree"}
		if _, err := app.updatedEnvironmentConfig(config, existing); err != nil {
			t.Errorf("%s: dialog refused an in-pod path: %v", envType, err)
		}
	}
}
