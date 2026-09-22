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

// displayShapedStates are the strings a tenant and an environment take on when
// they are read rather than resolved: the pair as it renders for a human, and
// that pair with the MCP local port an operator sees beside it. Joined into a
// path instead of refused, each one creates a directory of its own — empty,
// beside the real tenant/environment tree — which is how eight of them
// accumulated under the state root.
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

// TestDisplayShapedTenantAndEnvironmentNamesCreateNoStateDirectory runs the
// commands for real — not --dry-run — and diffs the config directory before and
// after. The refusal has to happen where the state path is built, so that no
// amount of command wiring can turn a label into a directory: a run naming a
// tenant/environment this way must fail and leave the tree exactly as it found
// it. The command is expected to fail; a zero exit would mean a label was
// accepted as a target.
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

// TestInitRefusesADisplayShapedTenantBeforeCreatingAnything covers the other
// route to the same residue: a tenant name that reads as a pair, handed to the
// one command that creates a tenant. Init writes the root tool config on its
// way in — that file is not residue and not what this asserts — so the
// assertion is the invariant itself: no component of the state tree carries
// whitespace, and no directory named after the pair exists.
func TestInitRefusesADisplayShapedTenantBeforeCreatingAnything(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		args    []string
		dirName []string
	}{
		{
			// The tenant slot holds a pair, so init's own tenant-name rule and
			// the state-path segment check both refuse it.
			name:    "tenant_slot_holds_a_pair",
			args:    []string{"init", "erun ux", "frs local"},
			dirName: []string{"erun", "erun ux"},
		},
		{
			// A valid tenant with a display-shaped environment, which no
			// tenant-name rule covers: the refusal has to be the state path's
			// own, or the directory appears beside the real environment.
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

// assertNoWhitespaceStateComponent walks the state tree and fails on any
// component that carries whitespace, whichever command left it — a directory
// that merely came to exist is the residue being guarded against, so this walks
// directories as well as files.
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
