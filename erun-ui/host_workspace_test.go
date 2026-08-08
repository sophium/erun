package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// hostWorkspaceStore builds a remote-agent env whose workspace-sync mirror lives
// at localPath, so the host-workspace methods resolve to a real host directory.
func hostWorkspaceStore(localPath string) stubUIStore {
	return stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev": {
				Name:              "dev",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Type:              eruncommon.EnvironmentTypeRemoteAgent,
				SSHD: eruncommon.SSHDConfig{
					Enabled: true,
					WorkspaceSync: eruncommon.SSHDWorkspaceSyncConfig{
						Enabled:   true,
						LocalPath: localPath,
					},
				},
			},
		},
	}
}

func TestResolveHostWorkspaceRequiresSyncedMirror(t *testing.T) {
	store := hostWorkspaceStore("/tmp/frs-mirror")
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.Enabled = false
	store.envs["frs/dev"] = env
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	if _, _, err := app.resolveHostWorkspace(uiSelection{Tenant: "frs", Environment: "dev"}); err == nil {
		t.Fatal("expected a remote-agent env without workspace sync to have no host workspace")
	}
}

func TestRunHostArtifactLaunchesArtifactWithinOutputsDir(t *testing.T) {
	local := t.TempDir()
	outputs := filepath.Join(local, eruncommon.WorkspaceSyncArtifactsSubdir)
	requireWorkspaceSyncNoError(t, os.MkdirAll(outputs, 0o755), "mkdir outputs")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(outputs, "erun-app.exe"), []byte("x"), 0o644), "write artifact")

	var launched, launchedDir string
	app := NewApp(erunUIDeps{
		store: hostWorkspaceStore(local),
		launchHostArtifact: func(exePath, dir string) error {
			launched = exePath
			launchedDir = dir
			return nil
		},
	})
	defer app.shutdown(context.Background())

	if err := app.RunHostArtifact(uiSelection{Tenant: "frs", Environment: "dev"}, "erun-app.exe"); err != nil {
		t.Fatalf("RunHostArtifact failed: %v", err)
	}
	if launched != filepath.Join(outputs, "erun-app.exe") {
		t.Fatalf("unexpected launched path: %q", launched)
	}
	if launchedDir != outputs {
		t.Fatalf("unexpected launch dir: %q", launchedDir)
	}
}

func TestRunHostArtifactRejectsMissingAndTraversal(t *testing.T) {
	local := t.TempDir()
	app := NewApp(erunUIDeps{
		store:              hostWorkspaceStore(local),
		launchHostArtifact: func(string, string) error { return nil },
	})
	defer app.shutdown(context.Background())

	if err := app.RunHostArtifact(uiSelection{Tenant: "frs", Environment: "dev"}, "../escape.exe"); err == nil {
		t.Fatal("expected a traversal path to be rejected")
	}
	if err := app.RunHostArtifact(uiSelection{Tenant: "frs", Environment: "dev"}, "missing.exe"); err == nil {
		t.Fatal("expected a missing artifact to error")
	}
}

func TestLoadHostDiffReadsHostWorktreeWithoutMCP(t *testing.T) {
	local := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", local}, args...)...)
		requireWorkspaceSyncNoError(t, cmd.Run(), "git "+strings.Join(args, " "))
	}
	runGit("init")
	runGit("config", "user.email", "t@example")
	runGit("config", "user.name", "t")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n"), 0o644), "write baseline")
	runGit("add", ".")
	runGit("commit", "-m", "init")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644), "modify")

	// No loadDiff/MCP dep is wired: a host diff must never dial the in-pod MCP.
	app := NewApp(erunUIDeps{store: hostWorkspaceStore(local)})
	defer app.shutdown(context.Background())

	diff, err := app.LoadHostDiff(uiSelection{Tenant: "frs", Environment: "dev"}, uiDiffOptions{})
	requireWorkspaceSyncNoError(t, err, "load host diff")
	if diff.WorkingDirectory != local {
		t.Fatalf("expected working dir %q, got %q", local, diff.WorkingDirectory)
	}
	foundMain := false
	for _, f := range diff.Files {
		if f.Path == "main.go" {
			foundMain = true
		}
	}
	if !foundMain {
		t.Fatalf("expected main.go in the host diff, got %+v", diff.Files)
	}
}
