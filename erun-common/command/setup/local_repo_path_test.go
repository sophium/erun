package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/open"
)

// hostRepoPathFixture is shared with erun-ui/environment_config_localrepo_test.go
// so both sides reach one verdict per value.
type hostRepoPathFixture struct {
	name     string
	path     string
	accepted bool
}

func hostRepoPathFixtures(t *testing.T) []hostRepoPathFixture {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "not-a-directory.txt")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatalf("write fixture file: %v", err)
	}
	return []hostRepoPathFixture{
		{name: "existing absolute directory", path: dir, accepted: true},
		{name: "existing directory with trailing separator", path: dir + string(os.PathSeparator), accepted: true},
		{name: "padded but real directory", path: "  " + dir + "  ", accepted: true},
		{name: "empty", path: "", accepted: false},
		{name: "blank", path: "   ", accepted: false},
		{name: "relative", path: filepath.Join("relative", "repo"), accepted: false},
		{name: "dot-relative", path: "." + string(os.PathSeparator) + "repo", accepted: false},
		{name: "missing absolute", path: filepath.Join(dir, "does-not-exist-2383"), accepted: false},
		{name: "absolute path to a file", path: file, accepted: false},
	}
}

func TestInitRetypeRefusesTheSamePaths(t *testing.T) {
	for _, tc := range hostRepoPathFixtures(t) {
		state := &bootstrapRunState{
			tenant:  "acme",
			envName: "dev",
			params:  open.BootstrapInitParams{ProjectRoot: tc.path},
			// Pre-detected so the empty fixture reaches init's refusal.
			detected:  projectContext{loaded: true},
			envConfig: config.EnvConfig{Name: "dev", Type: config.EnvironmentTypeRemoteAgent},
		}
		err := state.adoptLocalRepoPathForType(config.EnvironmentTypeLocalAgent)
		switch {
		case tc.accepted && err != nil:
			t.Errorf("init / %s: refused %q, want accepted: %v", tc.name, tc.path, err)
		case !tc.accepted && err == nil:
			t.Errorf("init / %s: accepted %q, want refused", tc.name, tc.path)
		}
	}
}
