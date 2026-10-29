package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
)

// displayShapedStates are tenant/environment labels as rendered for a human; joined
// into a path they would each create a stray state directory.
var displayShapedStates = []struct {
	name string
	args []string
}{
	{
		name: "pair_reads_as_one_label_in_the_tenant_slot",
		args: []string{"pin", "erun ux", "frs local", "--version", "1.0.0"},
	},
	{
		name: "pair_with_a_port_reads_as_one_label_in_the_environment_slot",
		args: []string{"pin", "frs", "local 17700", "--version", "1.0.0"},
	},
	{
		name: "pair_reads_as_one_label_for_a_stop",
		args: []string{"stop", "erun ux", "frs local"},
	},
}

// TestDisplayShapedTenantAndEnvironmentNamesCreateNoStateDirectory runs for real and
// requires the config tree to be unchanged.
func TestDisplayShapedTenantAndEnvironmentNamesCreateNoStateDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range displayShapedStates {
		t.Run(tc.name, func(t *testing.T) {
			setup := env.New(t)
			before := snapshotConfigTree(t, setup.ConfigHome)
			result := erun.Run(t, tc.args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			after := snapshotConfigTree(t, setup.ConfigHome)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%v mutated the config directory\nbefore: %v\nafter:  %v\noutput:\n%s",
					tc.args, before, after, result.Combined)
			}
			if result.ExitCode == 0 {
				t.Fatalf("%v succeeded for a name that is not a path segment\noutput:\n%s", tc.args, result.Combined)
			}
		})
	}
}

// TestInitRefusesADisplayShapedTenantBeforeCreatingAnything: init writes the root tool
// config regardless, so this asserts no whitespace state component instead.
func TestInitRefusesADisplayShapedTenantBeforeCreatingAnything(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		args    []string
		dirName []string
	}{
		{
			name:    "tenant_slot_holds_a_pair",
			args:    []string{"init", "erun ux", "frs local"},
			dirName: []string{"erun", "erun ux"},
		},
		{
			// No tenant-name rule covers the environment; the state path check must refuse it.
			name:    "environment_slot_holds_a_pair",
			args:    []string{"init", "probe", "frs local"},
			dirName: []string{"erun", "probe", "frs local"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			setup := env.New(t)
			args := append(append([]string{}, tc.args...),
				"--type", "host",
				"--project-root", setup.Cwd,
				"--confirm-environment=true",
				"--yes",
			)
			result := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			assertNoWhitespaceStateComponent(t, setup.ConfigHome)
			if _, err := os.Stat(filepath.Join(append([]string{setup.ConfigHome}, tc.dirName...)...)); !os.IsNotExist(err) {
				t.Fatalf("%v created a state directory named after the pair (stat: %v)\noutput:\n%s", args, err, result.Combined)
			}
			if result.ExitCode == 0 {
				t.Fatalf("%v succeeded for a name that is not a path segment\noutput:\n%s", args, result.Combined)
			}
		})
	}
}

// assertNoWhitespaceStateComponent walks directories too: an empty directory is the residue.
func assertNoWhitespaceStateComponent(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return relErr
		}
		for _, component := range strings.Split(filepath.ToSlash(rel), "/") {
			if strings.ContainsAny(component, " \t\n\r\v\f") {
				t.Errorf("%s carries whitespace in a path component, so the state tree holds a display-shaped name", rel)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
