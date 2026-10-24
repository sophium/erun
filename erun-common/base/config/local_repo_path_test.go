package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// hostRepoPathFixture is a candidate path and its verdict; the desktop's Manage
// save path is held to the same fixtures.
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
