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

// dryRunPurityCases run mutating commands against an unseeded config root; a dry
// run must not create even an empty directory.
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
		// Validation must fail before anything is created.
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
	{
		// A pull-as-new would create a config tree; a dry run must not.
		name: "platform_env_push_nonexistent_environment",
		args: []string{
			"platform", "env", "push", "purity-tenant", "purity-env",
			"--dry-run",
		},
	},
	{
		name: "platform_env_pull_nonexistent_environment",
		args: []string{
			"platform", "env", "pull", "purity-tenant", "purity-env",
			"--environment-id", "018f0000-0000-7000-8000-000000000000",
			"--repo-path", "/tmp",
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

// snapshotConfigTree lists every entry under root, directories included.
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
