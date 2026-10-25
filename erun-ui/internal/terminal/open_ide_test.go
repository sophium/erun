package terminal

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestOpenIDERunsWithoutConsumingTerminalWhenSSHDEnabled(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
				Type:              configpkg.EnvironmentTypeRuntime,
				SSHD: configpkg.SSHDConfig{
					Enabled: true,
				},
			},
		},
	}

	startCalls := 0
	var started StartParams
	app := newTestService(Deps{
		Store:          store,
		ResolveCLIPath: func() string { return "/tmp/erun" },
		StartTerminal: func(params StartParams) (PTY, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
		RunIDECommand: func(_ context.Context, params StartParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer closeTestService(app)

	if err := app.OpenIDE(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, "vscode"); err != nil {
		t.Fatalf("OpenIDE failed: %v", err)
	}
	if startCalls != 0 {
		t.Fatalf("expected IDE open not to start a managed terminal, got %d calls", startCalls)
	}
	if started.Executable != "/tmp/erun" {
		t.Fatalf("unexpected executable: %q", started.Executable)
	}
	wantArgs := []string{"open", "erun", "remote", "--vscode"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
}

func TestOpenIDEOpensLocalProjectWithoutSSHD(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	var started StartParams
	app := newTestService(Deps{
		Store:          store,
		ResolveCLIPath: func() string { return "/tmp/erun" },
		RunIDECommand: func(_ context.Context, params StartParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer closeTestService(app)

	if err := app.OpenIDE(uimodel.Selection{Tenant: "erun", Environment: "local"}, "intellij"); err != nil {
		t.Fatalf("OpenIDE failed: %v", err)
	}
	wantExecutable, wantArgs, err := LocalOpenIDECommand(runtime.GOOS, "intellij", projectRoot)
	if err != nil {
		t.Fatalf("LocalOpenIDECommand failed: %v", err)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected dir: %q", started.Dir)
	}
	if started.Executable != wantExecutable {
		t.Fatalf("unexpected executable: got %q want %q", started.Executable, wantExecutable)
	}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
}

func TestLocalOpenIDECommandBuildsDarwinCommands(t *testing.T) {
	projectRoot := "/tmp/tenant-a"

	executable, args, err := LocalOpenIDECommand("darwin", "vscode", projectRoot)
	if err != nil {
		t.Fatalf("LocalOpenIDECommand vscode failed: %v", err)
	}
	if executable != "open" || strings.Join(args, "\n") != strings.Join([]string{"-a", "Visual Studio Code", projectRoot}, "\n") {
		t.Fatalf("unexpected VS Code command: %s %+v", executable, args)
	}

	executable, args, err = LocalOpenIDECommand("darwin", "intellij", projectRoot)
	if err != nil {
		t.Fatalf("LocalOpenIDECommand intellij failed: %v", err)
	}
	if executable != "open" || strings.Join(args, "\n") != strings.Join([]string{"-a", "IntelliJ IDEA", projectRoot}, "\n") {
		t.Fatalf("unexpected IntelliJ command: %s %+v", executable, args)
	}
}

func TestOpenIDERejectsMissingSSHDWithoutHiddenInit(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}

	var started StartParams
	app := newTestService(Deps{
		Store:          store,
		ResolveCLIPath: func() string { return "/tmp/erun" },
		RunIDECommand: func(_ context.Context, params StartParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer closeTestService(app)

	err := app.OpenIDE(uimodel.Selection{Tenant: "erun", Environment: "remote"}, "intellij")
	if err == nil {
		t.Fatal("expected missing SSHD error")
	}
	if !strings.Contains(err.Error(), "open intellij requires sshd-enabled remote environment") {
		t.Fatalf("unexpected error: %v", err)
	}
	if started.Executable != "" || len(started.Args) != 0 {
		t.Fatalf("did not expect hidden SSHD init command, got %+v", started)
	}
}

func TestTerminalSessionExitReasonUsesProcessExitError(t *testing.T) {
	session := newStubTerminalSession()
	session.waitErr = io.ErrUnexpectedEOF

	got := terminalSessionExitReason(session, io.EOF)
	if got != io.ErrUnexpectedEOF.Error() {
		t.Fatalf("unexpected exit reason: got %q want %q", got, io.ErrUnexpectedEOF.Error())
	}
}

func TestTerminalSessionExitReasonIgnoresCleanEOF(t *testing.T) {
	got := terminalSessionExitReason(newStubTerminalSession(), io.EOF)
	if got != "" {
		t.Fatalf("unexpected clean exit reason: %q", got)
	}
}
