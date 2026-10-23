package integration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

// runtime-init is the erun-devops image's ENTRYPOINT. Its real run is an
// init that only makes sense inside the Linux runtime container (it is covered
// there by erun-common's runtime_supervisor_linux_test.go); what this suite pins
// is the plan a dry run shows for the injected environment and the refusal
// anywhere else.
func TestRuntimeInit(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"runtime-init", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "runtime-init/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_lists_the_devops_boot_for_the_injected_environment", func(t *testing.T) {
		// A cloud-managed runtime environment with sshd and the MCP edge on:
		// every step that applies is listed with its concrete paths and the
		// exact emcp invocation, and nothing is written.
		setup := env.New(t)
		result := erun.Run(t, []string{"runtime-init", "--dry-run"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: append(setup.Env(),
				"ERUN_TENANT=team", "ERUN_ENVIRONMENT=dev", "ERUN_ENV_TYPE=runtime",
				"ERUN_MCP_PORT=17000", "ERUN_SSHD_ENABLED=true", "ERUN_CLOUD_ENVIRONMENT=true",
				"ERUN_GO_BUILD_CACHE_MAX_BYTES=8589934592"),
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.Home, ".erun-shell-hook.bashrc")); !os.IsNotExist(err) {
			t.Fatalf("a dry run must not write the shell hook: %v", err)
		}
		golden.Equal(t, "runtime-init/dry_run_lists_the_devops_boot_for_the_injected_environment", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_runs_a_command_after_the_common_preparation", func(t *testing.T) {
		// Any command other than devops gets the preparation every mode shares
		// and then runs as the supervised foreground process.
		setup := env.New(t)
		result := erun.Run(t, []string{"runtime-init", "--dry-run", "--", "sh", "-c", "exit 3"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "runtime-init/dry_run_runs_a_command_after_the_common_preparation", normalize.Apply(result.Combined))
	})

	t.Run("refuses_a_host_that_is_not_linux", func(t *testing.T) {
		// An init's duties are Linux process semantics; anywhere else the
		// command names its one venue. Pinned to darwin so the golden holds on
		// every host.
		setup := env.New(t)
		result := erun.Run(t, []string{"runtime-init", "--", "true"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=darwin"),
		})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal off Linux, got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "runtime-init/refuses_a_host_that_is_not_linux", normalize.Apply(result.Combined))
	})
}

// seedGoBuildCache lays out count go cache entries of size bytes each, the
// lowest index the least recently used.
func seedGoBuildCache(t *testing.T, dir string, count, size int) {
	t.Helper()
	for i := 0; i < count; i++ {
		sub := filepath.Join(dir, fmt.Sprintf("%02x", i))
		if err := os.MkdirAll(sub, 0o755); err != nil {
			t.Fatal(err)
		}
		hash := fmt.Sprintf("%064x", i)
		stamp := time.Now().Add(-time.Duration(count-i) * time.Hour)
		for name, body := range map[string][]byte{hash + "-d": make([]byte, size), hash + "-a": []byte("v1\n")} {
			path := filepath.Join(sub, name)
			if err := os.WriteFile(path, body, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, stamp, stamp); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "trim.txt"), []byte("200\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTrimCache(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"trim-cache", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "trim-cache/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_counts_the_entries_it_would_evict", func(t *testing.T) {
		// Ten 1000-byte entries (plus 3-byte action files) against a 5000-byte
		// cap: the five oldest would go, and nothing is removed.
		setup := env.New(t)
		cache := filepath.Join(setup.Home, "go-build")
		seedGoBuildCache(t, cache, 10, 1000)
		result := erun.Run(t, []string{"trim-cache", "--dry-run", "--max-bytes", "5000", cache}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(cache, "00", fmt.Sprintf("%064x", 0)+"-d")); err != nil {
			t.Fatalf("a dry run must remove nothing: %v", err)
		}
		golden.Equal(t, "trim-cache/dry_run_counts_the_entries_it_would_evict", normalize.Apply(result.Combined))
	})

	t.Run("real_run_evicts_the_least_recently_used_entries", func(t *testing.T) {
		// The cap and the cache come from the environment the runtime chart
		// renders (GOCACHE, ERUN_GO_BUILD_CACHE_MAX_BYTES); the oldest entries
		// go, the newest and the cache's bookkeeping stay, and --output json
		// carries the result.
		setup := env.New(t)
		cache := filepath.Join(setup.Home, "go-build")
		seedGoBuildCache(t, cache, 10, 1000)
		result := erun.Run(t, []string{"--output", "json", "trim-cache"}, erun.RunOptions{
			Cwd: setup.Cwd,
			Env: append(setup.Env(), "GOCACHE="+cache, "ERUN_GO_BUILD_CACHE_MAX_BYTES=5000"),
		})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		for _, kept := range []string{filepath.Join("09", fmt.Sprintf("%064x", 9)+"-d"), "trim.txt"} {
			if _, err := os.Stat(filepath.Join(cache, kept)); err != nil {
				t.Fatalf("%s should survive the trim: %v", kept, err)
			}
		}
		if _, err := os.Stat(filepath.Join(cache, "00", fmt.Sprintf("%064x", 0)+"-d")); !os.IsNotExist(err) {
			t.Fatalf("the oldest entry should be evicted: %v", err)
		}
		golden.Equal(t, "trim-cache/real_run_evicts_the_least_recently_used_entries", normalize.Apply(result.Combined))
	})

	t.Run("a_cap_of_zero_is_no_bound", func(t *testing.T) {
		// The chart renders 0 to turn the bound off; read as a bound of zero it
		// would clear the cache.
		setup := env.New(t)
		cache := filepath.Join(setup.Home, "go-build")
		seedGoBuildCache(t, cache, 2, 1000)
		result := erun.Run(t, []string{"trim-cache", cache}, erun.RunOptions{Cwd: setup.Cwd, Env: append(setup.Env(), "ERUN_GO_BUILD_CACHE_MAX_BYTES=0")})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "trim-cache/a_cap_of_zero_is_no_bound", normalize.Apply(result.Combined))
	})

	t.Run("refuses_a_cap_that_is_not_positive", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"trim-cache", "--max-bytes", "-5", filepath.Join(setup.Home, "go-build")}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode == 0 {
			t.Fatalf("expected a refusal, got exit 0:\n%s", result.Combined)
		}
		golden.Equal(t, "trim-cache/refuses_a_cap_that_is_not_positive", normalize.Apply(result.Combined))
	})
}

func seedAgentAssets(t *testing.T, root string) (skills, agents string) {
	t.Helper()
	skills = filepath.Join(root, "skills")
	agents = filepath.Join(root, "agents")
	for path, body := range map[string]string{
		filepath.Join(skills, "erun-build-env", "SKILL.md"):                "v1\n",
		filepath.Join(skills, "erun-build-env", "templates", "Dockerfile"): "FROM scratch\n",
		filepath.Join(skills, "not-a-skill", "README.md"):                  "stray\n",
		filepath.Join(agents, "erun-builder.md"):                           "builder v1\n",
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return skills, agents
}

func TestInstallSkills(t *testing.T) {
	t.Parallel()

	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"install-skills", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "install-skills/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_installs_nothing", func(t *testing.T) {
		setup := env.New(t)
		skills, agents := seedAgentAssets(t, filepath.Join(setup.Cwd, "erun-skills"))
		result := erun.Run(t, []string{"install-skills", "--dry-run", "--skills-dir", skills, "--agents-dir", agents}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		if _, err := os.Stat(filepath.Join(setup.Home, ".claude", "skills")); !os.IsNotExist(err) {
			t.Fatalf("a dry run must install nothing: %v", err)
		}
		golden.Equal(t, "install-skills/dry_run_installs_nothing", normalize.Apply(result.Combined))
	})

	t.Run("installs_then_keeps_an_operator_edit", func(t *testing.T) {
		// The first run installs into both assistants; after the source moves
		// on and the operator edits the installed skill, the second run keeps
		// the edit and still refreshes the untouched agent.
		setup := env.New(t)
		skills, agents := seedAgentAssets(t, filepath.Join(setup.Cwd, "erun-skills"))
		args := []string{"install-skills", "--skills-dir", skills, "--agents-dir", agents}
		first := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if first.ExitCode != 0 {
			t.Fatalf("exit %d: %s", first.ExitCode, first.Combined)
		}
		installed := filepath.Join(setup.Home, ".claude", "skills", "erun-build-env", "SKILL.md")
		for path, body := range map[string]string{
			installed: "operator edit\n",
			filepath.Join(skills, "erun-build-env", "SKILL.md"): "v2\n",
			filepath.Join(agents, "erun-builder.md"):            "builder v2\n",
		} {
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		second := erun.Run(t, args, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if second.ExitCode != 0 {
			t.Fatalf("exit %d: %s", second.ExitCode, second.Combined)
		}
		if data, _ := os.ReadFile(installed); string(data) != "operator edit\n" {
			t.Fatalf("the operator's edit must be kept, got %q", data)
		}
		golden.Equal(t, "install-skills/installs_then_keeps_an_operator_edit", normalize.Apply(first.Combined+"--- second run ---\n"+second.Combined))
	})
}
