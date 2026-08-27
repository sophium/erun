package main

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestHostOpenCommandPerOS(t *testing.T) {
	cases := []struct {
		goos       string
		executable string
		args       []string
	}{
		{"darwin", "open", []string{"/tmp/x.xlsx"}},
		{"linux", "xdg-open", []string{"/tmp/x.xlsx"}},
		{"windows", "explorer", []string{"/tmp/x.xlsx"}},
	}
	for _, c := range cases {
		executable, args, err := hostOpenCommand(c.goos, "/tmp/x.xlsx")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", c.goos, err)
		}
		if executable != c.executable {
			t.Fatalf("%s: expected executable %q, got %q", c.goos, c.executable, executable)
		}
		if len(args) != 1 || args[0] != "/tmp/x.xlsx" {
			t.Fatalf("%s: expected the path passed as a single argument, got %v", c.goos, args)
		}
	}
	if _, _, err := hostOpenCommand("plan9", "/tmp/x"); err == nil {
		t.Fatal("expected an unsupported OS to error")
	}
}

func TestHostRevealCommandPerOS(t *testing.T) {
	cases := []struct {
		goos       string
		target     string
		executable string
		args       []string
	}{
		{"darwin", "/tmp/x.xlsx", "open", []string{"-R", "/tmp/x.xlsx"}},
		{"linux", "/tmp/dir/x.xlsx", "xdg-open", []string{"/tmp/dir"}},
		{"windows", `C:\Users\me\x.xlsx`, "explorer", []string{"/select,", `C:\Users\me\x.xlsx`}},
	}
	for _, c := range cases {
		executable, args, err := hostRevealCommand(c.goos, c.target)
		requireWorkspaceSyncNoError(t, err, c.goos+" reveal command")
		if executable != c.executable {
			t.Fatalf("%s: expected executable %q, got %q", c.goos, c.executable, executable)
		}
		if !slices.Equal(args, c.args) {
			t.Fatalf("%s: expected args %v, got %v", c.goos, c.args, args)
		}
	}
}

func TestOpenHostPathPassesArgumentNeverShell(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "chip-migration-audit.xlsx")
	requireWorkspaceSyncNoError(t, os.WriteFile(target, []byte("x"), 0o644), "write target")

	var gotExecutable string
	var gotArgs []string
	app := NewApp(erunUIDeps{
		store: stubUIStore{},
		launchHostOpener: func(executable string, args []string) error {
			gotExecutable = executable
			gotArgs = args
			return nil
		},
	})
	defer app.shutdown(context.Background())

	if err := app.OpenHostPath(target); err != nil {
		t.Fatalf("OpenHostPath failed: %v", err)
	}
	if gotExecutable == "" {
		t.Fatal("expected an opener to be launched")
	}
	if len(gotArgs) != 1 || gotArgs[0] != target {
		t.Fatalf("expected the path as a single opaque argument, got %v", gotArgs)
	}
}

func TestOpenHostPathRejectsMissingFile(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		launchHostOpener: func(string, []string) error { return nil },
	})
	defer app.shutdown(context.Background())

	if err := app.OpenHostPath(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("expected a missing host file to error")
	}
}

func TestResolveEnvironmentHostPathMapsPodWorktreeToMirror(t *testing.T) {
	local := t.TempDir()
	requireWorkspaceSyncNoError(t, os.MkdirAll(filepath.Join(local, "erun-ui"), 0o755), "mkdir")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(local, "erun-ui", "app.go"), []byte("package main\n"), 0o644), "write file")

	app := NewApp(erunUIDeps{store: hostWorkspaceStore(local)})
	defer app.shutdown(context.Background())

	resolution, err := app.ResolveEnvironmentHostPath(uiSelection{Tenant: "frs", Environment: "dev"}, "/home/erun/git/frs/erun-ui/app.go")
	requireWorkspaceSyncNoError(t, err, "resolve pod path")
	if resolution.Kind != "mirror" {
		t.Fatalf("expected kind mirror, got %+v", resolution)
	}
	if resolution.HostPath != filepath.Join(local, "erun-ui", "app.go") {
		t.Fatalf("unexpected host path: %+v", resolution)
	}
}

func TestResolveEnvironmentHostPathMapsOutputsToArtifactMirror(t *testing.T) {
	local := t.TempDir()
	outputs := filepath.Join(local, eruncommon.WorkspaceSyncArtifactsSubdir)
	requireWorkspaceSyncNoError(t, os.MkdirAll(outputs, 0o755), "mkdir outputs")
	requireWorkspaceSyncNoError(t, os.WriteFile(filepath.Join(outputs, "erun-app.exe"), []byte("x"), 0o644), "write artifact")

	app := NewApp(erunUIDeps{store: hostWorkspaceStore(local)})
	defer app.shutdown(context.Background())

	resolution, err := app.ResolveEnvironmentHostPath(uiSelection{Tenant: "frs", Environment: "dev"}, eruncommon.DefaultRuntimeOutputsDir+"/erun-app.exe")
	requireWorkspaceSyncNoError(t, err, "resolve pod path")
	if resolution.Kind != "artifact" {
		t.Fatalf("expected kind artifact, got %+v", resolution)
	}
	if resolution.HostPath != filepath.Join(outputs, "erun-app.exe") {
		t.Fatalf("unexpected host path: %+v", resolution)
	}
}

// The regression #1354 exists to prevent: a pod path that happens to share a
// name with a real host file (/etc/hosts is the canonical example) must never
// resolve to that host file just because it falls outside the recognized pod
// directories.
func TestResolveEnvironmentHostPathNeverResolvesArbitraryPodPathToHost(t *testing.T) {
	local := t.TempDir()
	app := NewApp(erunUIDeps{store: hostWorkspaceStore(local)})
	defer app.shutdown(context.Background())

	resolution, err := app.ResolveEnvironmentHostPath(uiSelection{Tenant: "frs", Environment: "dev"}, "/etc/hosts")
	requireWorkspaceSyncNoError(t, err, "resolve pod path")
	if resolution.Kind != "unresolved" {
		t.Fatalf("expected /etc/hosts to stay unresolved, got %+v", resolution)
	}
	if resolution.HostPath != "" {
		t.Fatalf("expected no host path on an unresolved result, got %+v", resolution)
	}
	if resolution.Reason == "" {
		t.Fatal("expected a stated reason for the unresolved path")
	}
}

func TestResolveEnvironmentHostPathReportsNoWorkspaceWhenSyncDisabled(t *testing.T) {
	store := hostWorkspaceStore("/tmp/frs-mirror")
	env := store.envs["frs/dev"]
	env.SSHD.WorkspaceSync.Enabled = false
	store.envs["frs/dev"] = env
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	resolution, err := app.ResolveEnvironmentHostPath(uiSelection{Tenant: "frs", Environment: "dev"}, "/home/erun/git/frs/main.go")
	requireWorkspaceSyncNoError(t, err, "resolve pod path")
	if resolution.Kind != "unresolved" || resolution.Reason == "" {
		t.Fatalf("expected an unresolved result with a reason, got %+v", resolution)
	}
}

func TestResolveEnvironmentHostPathRequiresTenantAndEnvironment(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())

	if _, err := app.ResolveEnvironmentHostPath(uiSelection{}, "/etc/hosts"); err == nil {
		t.Fatal("expected missing tenant/environment to error")
	}
}
