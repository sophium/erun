package integration

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
)

// dryRunPurityCases are mutating commands run against a config root that does
// not yet hold the named tenant/environment — the exact shape that let stray
// "add", "push", "runtime", and "nonexistent-1185" directories accumulate
// under ~/.config/erun for months. Root AGENTS.md documents --dry-run
// everywhere as "resolve and trace mutating actions without executing them";
// a run that creates so much as an empty directory breaks that guarantee as
// surely as one that writes a file. This is a cross-command invariant: rather
// than trust each command's own goldens to notice a stray mkdir, diff the
// config directory's entry list before and after.
var dryRunPurityCases = []struct {
	name string
	args []string
}{
	{
		name: "init_brand_new_tenant_and_environment",
		args: []string{
			"init", "purity-tenant", "purity-env",
			"--type", "host",
			"--set-default-tenant=true",
			"--confirm-environment=true",
			"--dry-run",
		},
	},
	{
		// A --type value invalid enough to fail before tenant/environment
		// resolution must leave nothing behind either — the validate-before-create
		// half of this class of bug, not just the dry-run half.
		name: "init_rejects_invalid_type_before_any_resolution",
		args: []string{
			"init", "purity-tenant", "purity-env",
			"--type", "invalid",
			"--dry-run",
		},
	},
	{
		name: "pin_nonexistent_tenant_and_environment",
		args: []string{
			"pin", "purity-tenant", "purity-env",
			"--version", "1.0.0",
			"--dry-run",
		},
	},
	{
		name: "deploy_nonexistent_tenant_and_environment",
		args: []string{
			"deploy", "purity-tenant", "purity-env",
			"--version", "1.0.0",
			"--dry-run",
		},
	},
}

func TestDryRunNeverTouchesTheConfigDirectory(t *testing.T) {
	t.Parallel()
	for _, tc := range dryRunPurityCases {
		t.Run(tc.name, func(t *testing.T) {
			setup := env.New(t)
			before := snapshotConfigTree(t, setup.ConfigHome)
			result := erun.Run(t, tc.args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
			after := snapshotConfigTree(t, setup.ConfigHome)
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("%v mutated the config directory\nbefore: %v\nafter:  %v\noutput:\n%s",
					tc.args, before, after, result.Combined)
			}
		})
	}
}

// snapshotConfigTree lists every entry under root, relative to root, so a
// command's filesystem footprint under the config directory can be diffed
// before and after it runs. A directory that merely comes to exist — with no
// file ever written into it — is exactly the kind of residue this guards
// against, so this must walk directories too, not just check for new files.
func snapshotConfigTree(t *testing.T, root string) []string {
	t.Helper()
	var entries []string
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
		entries = append(entries, rel)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	sort.Strings(entries)
	return entries
}
