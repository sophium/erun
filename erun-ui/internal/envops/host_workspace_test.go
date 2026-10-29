package envops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func hostWorkspaceStore(localPath string) fakeStore {
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"frs/dev": {
				Name:              "dev",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-dev",
				Type:              config.EnvironmentTypeRemoteAgent,
				SSHD: config.SSHDConfig{
					Enabled: true,
					WorkspaceSync: model.SSHDWorkspaceSyncConfig{
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
	service := newTestService(Deps{Store: store})

	if _, _, err := service.ResolveHostWorkspace(uimodel.Selection{Tenant: "frs", Environment: "dev"}); err == nil {
		t.Fatal("expected a remote-agent env without workspace sync to have no host workspace")
	}
}

func TestRunHostArtifactLaunchesArtifactWithinOutputsDir(t *testing.T) {
	local := t.TempDir()
	outputs := filepath.Join(local, workspace.WorkspaceSyncArtifactsSubdir)
	requireNoError(t, os.MkdirAll(outputs, 0o755), "mkdir outputs")
	requireNoError(t, os.WriteFile(filepath.Join(outputs, "erun-app.exe"), []byte("x"), 0o644), "write artifact")

	var launched, launchedDir string
	service := newTestService(Deps{
		Store: hostWorkspaceStore(local),
		LaunchHostArtifact: func(exePath, dir string) error {
			launched = exePath
			launchedDir = dir
			return nil
		},
	})

	if err := service.RunHostArtifact(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "erun-app.exe"); err != nil {
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
	service := newTestService(Deps{
		Store:              hostWorkspaceStore(local),
		LaunchHostArtifact: func(string, string) error { return nil },
	})

	if err := service.RunHostArtifact(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "../escape.exe"); err == nil {
		t.Fatal("expected a traversal path to be rejected")
	}
	if err := service.RunHostArtifact(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "missing.exe"); err == nil {
		t.Fatal("expected a missing artifact to error")
	}
}

func hostTypeEnvironmentStore(localPath string) fakeStore {
	return fakeStore{
		tenants: map[string]model.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]config.EnvConfig{
			"frs/dev": {
				Name:          "dev",
				LocalRepoPath: localPath,
				Type:          config.EnvironmentTypeHost,
			},
		},
	}
}

func TestHostWorkspacePathIsTheRepoPathDirectlyForAHostEnvironment(t *testing.T) {
	local := t.TempDir()
	store := hostTypeEnvironmentStore(local)
	service := newTestService(Deps{Store: store})

	_, path, err := service.ResolveHostWorkspace(uimodel.Selection{Tenant: "frs", Environment: "dev"})
	requireNoError(t, err, "resolve host workspace for a host-type env")
	if path != local {
		t.Fatalf("expected host workspace path %q, got %q", local, path)
	}
}

func TestLoadHostDiffReadsHostWorktreeWithoutMCP(t *testing.T) {
	local := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", local}, args...)...)
		requireNoError(t, cmd.Run(), "git "+strings.Join(args, " "))
	}
	runGit("init")
	runGit("config", "user.email", "t@example")
	runGit("config", "user.name", "t")
	requireNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n"), 0o644), "write baseline")
	runGit("add", ".")
	runGit("commit", "-m", "init")
	requireNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644), "modify")

	// No loadDiff/MCP dep is wired: a host diff must never dial the in-pod MCP.
	service := newTestService(Deps{Store: hostWorkspaceStore(local)})

	diff, err := service.LoadHostDiff(uimodel.Selection{Tenant: "frs", Environment: "dev"}, uimodel.DiffOptions{})
	requireNoError(t, err, "load host diff")
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

func TestLoadDirectoryDiffReadsADirectoryWithoutMCP(t *testing.T) {
	local := t.TempDir()
	runGit := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", local}, args...)...)
		requireNoError(t, cmd.Run(), "git "+strings.Join(args, " "))
	}
	runGit("init")
	runGit("config", "user.email", "t@example")
	runGit("config", "user.name", "t")
	requireNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n"), 0o644), "write baseline")
	runGit("add", ".")
	runGit("commit", "-m", "init")
	requireNoError(t, os.WriteFile(filepath.Join(local, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644), "modify")

	service := newTestService(Deps{})

	diff, err := service.LoadDirectoryDiff(local, uimodel.DiffOptions{})
	requireNoError(t, err, "load directory diff")
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
		t.Fatalf("expected main.go in the directory diff, got %+v", diff.Files)
	}
}

func TestLoadDirectoryDiffRefusesAnUnusablePath(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a-file")
	requireNoError(t, os.WriteFile(file, []byte("x"), 0o644), "write file")
	missing := filepath.Join(dir, "not-there")

	service := newTestService(Deps{})

	if _, err := service.LoadDirectoryDiff("", uimodel.DiffOptions{}); err == nil {
		t.Fatal("expected an empty path to be refused")
	}
	if _, err := service.LoadDirectoryDiff(missing, uimodel.DiffOptions{}); err == nil {
		t.Fatal("expected a missing directory to be refused")
	} else if !strings.Contains(err.Error(), missing) {
		t.Fatalf("refusal should name the path, got %q", err)
	}
	if _, err := service.LoadDirectoryDiff(file, uimodel.DiffOptions{}); err == nil {
		t.Fatal("expected a file rather than a directory to be refused")
	}
}
