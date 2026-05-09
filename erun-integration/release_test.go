package integration

import (
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func TestRelease(t *testing.T) {
	t.Run("help", func(t *testing.T) {
		setup := env.New(t)
		result := erun.Run(t, []string{"release", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/help", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_in_git_repo", func(t *testing.T) {
		setup := env.New(t)
		fixture.SeedGitRepo(t, setup.Cwd)
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		golden.Equal(t, "release/dry_run_in_git_repo", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_develop_emits_candidate_plan", func(t *testing.T) {
		// Exercises release.go on the develop branch: --dry-run must emit a
		// `mode=candidate` plan with sync-remote / push stages, the
		// resolved docker image reference, and the rc.<count> version
		// suffix on stdout.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_develop_emits_candidate_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_with_develop_emits_stable_plan", func(t *testing.T) {
		// Exercises release.go stable path on main when develop also
		// exists: --dry-run must include both sync-remote (main) and
		// sync-develop stages, and push to both branches.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		fixture.RunGit(t, setup.Cwd, "branch", "develop")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_with_develop_emits_stable_plan", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_main_without_develop_pushes_only_main", func(t *testing.T) {
		// Exercises release.go stable path on main when develop does not
		// exist: --dry-run must not include sync-develop or develop in
		// the push target.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_main_without_develop_pushes_only_main", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_includes_linux_release_scripts", func(t *testing.T) {
		// Exercises release.go linux package release path: --dry-run must
		// trace the per-component release script invocation with
		// ERUN_BUILD_VERSION when the host supports Linux package builds.
		// LinuxPackageBuildsSupported requires GOOS=linux and dpkg-deb in
		// PATH, so skip on hosts that cannot reach the support branch
		// (notably macOS dev machines).
		if runtime.GOOS != "linux" {
			t.Skip("linux release scripts only run on Linux hosts")
		}
		if _, err := osexec.LookPath("dpkg-deb"); err != nil {
			t.Skip("linux release scripts require dpkg-deb in PATH")
		}
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "develop")
		linuxComponentDir := filepath.Join(setup.Cwd, "erun-devops", "linux", "erun-cli")
		if err := os.MkdirAll(linuxComponentDir, 0o755); err != nil {
			t.Fatalf("mkdir linux component dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(linuxComponentDir, "release.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatalf("write release.sh: %v", err)
		}
		fixture.RunGit(t, setup.Cwd, "add", ".")
		fixture.RunGit(t, setup.Cwd, "commit", "-m", "add linux release script")

		result := erun.Run(t, []string{"release", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_includes_linux_release_scripts", normalize.Apply(result.Combined))
	})

	t.Run("dry_run_force_includes_tag_deletion_for_stale_release_tag", func(t *testing.T) {
		// Exercises release.go --force path: when the release tag already
		// exists remotely on a commit other than HEAD, --dry-run must
		// trace tag deletion (local + origin) before recreating the tag.
		setup := env.New(t)
		fixture.SeedReleaseRepo(t, setup.Cwd, "main")
		remoteRoot := filepath.Join(setup.Home, "origin.git")
		fixture.RunGit(t, setup.Home, "init", "-q", "--bare", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "remote", "add", "origin", remoteRoot)
		fixture.RunGit(t, setup.Cwd, "push", "-u", "origin", "main")
		fixture.RunGit(t, setup.Cwd, "tag", "-a", "v1.4.2", "-m", "Release 1.4.2")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "v1.4.2")
		// Advance HEAD past the tagged commit.
		fixture.RunGit(t, setup.Cwd, "commit", "--allow-empty", "-m", "advance head")
		fixture.RunGit(t, setup.Cwd, "push", "origin", "main")

		result := erun.Run(t, []string{"release", "--dry-run", "--force"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
		if result.ExitCode != 0 {
			t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
		}
		golden.Equal(t, "release/dry_run_force_includes_tag_deletion_for_stale_release_tag", normalize.Apply(result.Combined))
	})
}
