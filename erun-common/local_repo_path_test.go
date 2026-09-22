package eruncommon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostRepoPathFixture is one value a caller might be handed as a host-machine
// repository path, with the verdict every caller must reach for it.
//
// The same fixture set is driven through the desktop's Manage save path in
// erun-ui/environment_config_localrepo_test.go, which asserts its verdict
// against this package's — so the two sides are held to one verdict per value
// rather than to two hand-written expectation lists that can drift apart.
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

// TestValidateHostRepoPathVerdicts pins the shared definition both callers use.
func TestValidateHostRepoPathVerdicts(t *testing.T) {
	for _, envType := range []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeHost} {
		for _, tc := range hostRepoPathFixtures(t) {
			err := ValidateHostRepoPath(envType, tc.path)
			switch {
			case tc.accepted && err != nil:
				t.Errorf("%s / %s: refused %q, want accepted: %v", envType, tc.name, tc.path, err)
			case !tc.accepted && err == nil:
				t.Errorf("%s / %s: accepted %q, want refused", envType, tc.name, tc.path)
			case err != nil && !strings.Contains(err.Error(), HostRepoPathRequirement(envType)):
				t.Errorf("%s / %s: refusal %q does not carry the shared reason %q",
					envType, tc.name, err, HostRepoPathRequirement(envType))
			}
		}
	}
}

// TestValidateEnvRepoPathLeavesOtherTypesAlone keeps the requirement on the two
// types that actually use a host directory: a remote or runtime env records an
// in-pod path that has no business existing on this machine.
func TestValidateEnvRepoPathLeavesOtherTypesAlone(t *testing.T) {
	for _, envType := range []EnvironmentType{EnvironmentTypeRemoteAgent, EnvironmentTypeRuntime} {
		for _, tc := range hostRepoPathFixtures(t) {
			if err := ValidateEnvRepoPath(envType, tc.path); err != nil {
				t.Errorf("%s / %s: %q carries no host repo path requirement, got %v",
					envType, tc.name, tc.path, err)
			}
		}
	}
	for _, envType := range []EnvironmentType{EnvironmentTypeLocalAgent, EnvironmentTypeHost} {
		for _, tc := range hostRepoPathFixtures(t) {
			if err := ValidateEnvRepoPath(envType, tc.path); (err == nil) != tc.accepted {
				t.Errorf("%s / %s: ValidateEnvRepoPath disagrees with the fixture verdict for %q (err=%v)",
					envType, tc.name, tc.path, err)
			}
		}
	}
}

// TestInitRetypeRefusesTheSamePaths drives init's own caller — the retype that
// already refused — through the identical fixture set, so the side the issue
// held up as correct is shown to be the side the dialog now matches.
func TestInitRetypeRefusesTheSamePaths(t *testing.T) {
	for _, tc := range hostRepoPathFixtures(t) {
		state := &bootstrapRunState{
			tenant:  "acme",
			envName: "dev",
			params:  BootstrapInitParams{ProjectRoot: tc.path},
			// A project already detected, so the empty fixture reaches init's
			// refusal instead of falling through to a project search that needs a
			// real working directory to run in.
			detected:  projectContext{loaded: true},
			envConfig: EnvConfig{Name: "dev", Type: EnvironmentTypeRemoteAgent},
		}
		err := state.adoptLocalRepoPathForType(EnvironmentTypeLocalAgent)
		switch {
		case tc.accepted && err != nil:
			t.Errorf("init / %s: refused %q, want accepted: %v", tc.name, tc.path, err)
		case !tc.accepted && err == nil:
			t.Errorf("init / %s: accepted %q, want refused", tc.name, tc.path)
		}
	}
}
