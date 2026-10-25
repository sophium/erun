package envops

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/command/workspace"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
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
		requireNoError(t, err, c.goos+" reveal command")
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
	requireNoError(t, os.WriteFile(target, []byte("x"), 0o644), "write target")

	var gotExecutable string
	var gotArgs []string
	service := newTestService(Deps{
		LaunchHostOpener: func(executable string, args []string) error {
			gotExecutable = executable
			gotArgs = args
			return nil
		},
	})

	if err := service.OpenHostPath(target); err != nil {
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
	service := newTestService(Deps{
		LaunchHostOpener: func(string, []string) error { return nil },
	})

	if err := service.OpenHostPath(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Fatal("expected a missing host file to error")
	}
}

func TestResolveEnvironmentHostPathMapsPodWorktreeToMirror(t *testing.T) {
	local := t.TempDir()
	requireNoError(t, os.MkdirAll(filepath.Join(local, "erun-ui"), 0o755), "mkdir")
	requireNoError(t, os.WriteFile(filepath.Join(local, "erun-ui", "app.go"), []byte("package main\n"), 0o644), "write file")

	service := newTestService(Deps{Store: hostWorkspaceStore(local)})

	resolution, err := service.ResolveEnvironmentHostPath(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "/home/erun/git/frs/erun-ui/app.go")
	requireNoError(t, err, "resolve pod path")
	if resolution.Kind != "mirror" {
		t.Fatalf("expected kind mirror, got %+v", resolution)
	}
	if resolution.HostPath != filepath.Join(local, "erun-ui", "app.go") {
		t.Fatalf("unexpected host path: %+v", resolution)
	}
}

func TestResolveEnvironmentHostPathMapsOutputsToArtifactMirror(t *testing.T) {
	local := t.TempDir()
	outputs := filepath.Join(local, workspace.WorkspaceSyncArtifactsSubdir)
	requireNoError(t, os.MkdirAll(outputs, 0o755), "mkdir outputs")
	requireNoError(t, os.WriteFile(filepath.Join(outputs, "erun-app.exe"), []byte("x"), 0o644), "write artifact")

	service := newTestService(Deps{Store: hostWorkspaceStore(local)})

	resolution, err := service.ResolveEnvironmentHostPath(uimodel.Selection{Tenant: "frs", Environment: "dev"}, core.DefaultRuntimeOutputsDir+"/erun-app.exe")
	requireNoError(t, err, "resolve pod path")
	if resolution.Kind != "artifact" {
		t.Fatalf("expected kind artifact, got %+v", resolution)
	}
	if resolution.HostPath != filepath.Join(outputs, "erun-app.exe") {
		t.Fatalf("unexpected host path: %+v", resolution)
	}
}

// /etc/hosts must never resolve to the host file.
func TestResolveEnvironmentHostPathNeverResolvesArbitraryPodPathToHost(t *testing.T) {
	local := t.TempDir()
	service := newTestService(Deps{Store: hostWorkspaceStore(local)})

	resolution, err := service.ResolveEnvironmentHostPath(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "/etc/hosts")
	requireNoError(t, err, "resolve pod path")
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
	service := newTestService(Deps{Store: store})

	resolution, err := service.ResolveEnvironmentHostPath(uimodel.Selection{Tenant: "frs", Environment: "dev"}, "/home/erun/git/frs/main.go")
	requireNoError(t, err, "resolve pod path")
	if resolution.Kind != "unresolved" || resolution.Reason == "" {
		t.Fatalf("expected an unresolved result with a reason, got %+v", resolution)
	}
}

func TestResolveEnvironmentHostPathRequiresTenantAndEnvironment(t *testing.T) {
	service := newTestService(Deps{})

	if _, err := service.ResolveEnvironmentHostPath(uimodel.Selection{}, "/etc/hosts"); err == nil {
		t.Fatal("expected missing tenant/environment to error")
	}
}
