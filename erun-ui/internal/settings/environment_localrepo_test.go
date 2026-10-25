package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/repopath"
)

// TestManageDialogRepoPathMatchesInit runs the shared fixtures from
// erun-common/base/config/local_repo_path_test.go through the dialog's save path.
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

	for _, envType := range []configpkg.EnvironmentType{
		configpkg.EnvironmentTypeLocalAgent,
		configpkg.EnvironmentTypeHost,
	} {
		for _, tc := range fixtures {
			svc := newTestService(Deps{})
			existing := configpkg.EnvConfig{Name: "dev", Type: envType}
			config := EnvironmentConfig{
				Name:          "dev",
				Type:          envType,
				LocalRepoPath: tc.path,
			}
			updated, err := svc.updatedEnvironmentConfig(config, existing)
			initErr := repopath.ValidateHostRepoPath(envType, tc.path)

			switch {
			case (err == nil) != (initErr == nil):
				t.Errorf("%s / %s: dialog err=%v but init err=%v for %q — the two sides disagree",
					envType, tc.name, err, initErr, tc.path)
			case err != nil && !strings.Contains(err.Error(), repopath.HostRepoPathRequirement(envType)):
				t.Errorf("%s / %s: dialog refusal %q does not carry init's reason %q",
					envType, tc.name, err, repopath.HostRepoPathRequirement(envType))
			case err == nil && strings.TrimSpace(updated.LocalRepoPath) != strings.TrimSpace(tc.path):
				t.Errorf("%s / %s: dialog accepted %q but saved %q",
					envType, tc.name, tc.path, updated.LocalRepoPath)
			}
		}
	}
}

func TestManageDialogRepoPathIgnoresOtherTypes(t *testing.T) {
	for _, envType := range []configpkg.EnvironmentType{
		configpkg.EnvironmentTypeRemoteAgent,
		configpkg.EnvironmentTypeRuntime,
	} {
		svc := newTestService(Deps{})
		existing := configpkg.EnvConfig{Name: "dev", Type: envType}
		config := EnvironmentConfig{Name: "dev", Type: envType, LocalRepoPath: "/in-pod/worktree"}
		if _, err := svc.updatedEnvironmentConfig(config, existing); err != nil {
			t.Errorf("%s: dialog refused an in-pod path: %v", envType, err)
		}
	}
}
