package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// One erun version is recorded in several places and they only work when they
// agree. These scenarios pin the contract of the motion that realigns them:
// which references it recognises, which it leaves alone, and that it never
// writes on a dry run.

// seedDriftedPins writes the shape a real tenant repo has, deliberately pinned
// to three different versions — the reported drift this command exists for.
func seedDriftedPins(t *testing.T, root, rootConfigDir string) {
	t.Helper()
	write := func(relative, body string) {
		full := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", relative, err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", relative, err)
		}
	}
	// A closed port, so version discovery is deterministic and offline. An
	// unreachable registry means "could not verify", which is what lets an
	// explicit target still pin.
	rootConfigFile := filepath.Join(rootConfigDir, "config.yaml")
	if existing, err := os.ReadFile(rootConfigFile); err == nil && !strings.Contains(string(existing), "runtimeregistry") {
		if err := os.WriteFile(rootConfigFile, append(existing, []byte("runtimeregistry:\n  baseurl: http://127.0.0.1:1\n  tokenurl: http://127.0.0.1:1\n")...), 0o644); err != nil {
			t.Fatalf("write root config: %v", err)
		}
	}
	write("terraform-team/dev/main.tf", "module \"edge\" {\n  source = \"git::https://github.com/sophium/erun.git//erun-devops/terraform-erun/modules/terraform-erun-cluster-edge?ref=v1.0.102\"\n}\n\nmodule \"own\" {\n  source = \"git::https://github.com/team/infra.git//modules/thing?ref=v9.9.9\"\n}\n")
	write("team-api/Chart.yaml", "apiVersion: v2\nname: team-api\nversion: 0.1.0\ndependencies:\n  - name: erun-backend-api\n    repository: oci://ghcr.io/sophium/charts\n    version: 1.0.106\n  - name: team-internal\n    repository: oci://ghcr.io/team/charts\n    version: 3.2.1\n")
}

func TestPin(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"pin", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "pin/help", normalize.Apply(result.Combined))
	})

	// A dry run resolves the whole plan — every site, old and new — and leaves
	// the tree exactly as it found it. A re-pin edits files across a repo, so
	// being able to see it first is the difference between a safe motion and a
	// leap.
	t.Run("dry_run_reports_every_site_and_writes_nothing", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))
		before, err := os.ReadFile(filepath.Join(setup.Cwd, "terraform-team", "dev", "main.tf"))
		if err != nil {
			t.Fatalf("read before: %v", err)
		}

		result := erun.Run(t, []string{"pin", "team", "dev", "--version", "1.0.175", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, want := range []string{
			"terraform-ref",
			"helm-dependency",
			"runtime-version",
			"1.0.175",
		} {
			if !strings.Contains(result.Combined, want) {
				t.Fatalf("the plan must name %q:\n%s", want, result.Combined)
			}
		}
		after, err := os.ReadFile(filepath.Join(setup.Cwd, "terraform-team", "dev", "main.tf"))
		if err != nil {
			t.Fatalf("read after: %v", err)
		}
		if string(before) != string(after) {
			t.Fatalf("a dry run must not write:\n%s", after)
		}
	})

	// Reverting needs somewhere to go. Asking for one before any re-pin has
	// happened must say so rather than silently doing nothing.
	t.Run("revert_without_a_recorded_pin_says_so", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedTenantEnv(t, setup, "team", "dev")
		seedDriftedPins(t, setup.Cwd, filepath.Join(setup.ConfigHome, "erun"))

		result := erun.Run(t, []string{"pin", "team", "dev", "--revert"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal, got exit 0: %s", result.Combined)
		}
		if !strings.Contains(result.Combined, "nothing to revert to") {
			t.Fatalf("the refusal must say why:\n%s", result.Combined)
		}
	})
}
