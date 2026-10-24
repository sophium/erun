package main

import (
	"context"
	"io"
	"runtime"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

func TestOpenIDERunsWithoutConsumingTerminalWhenSSHDEnabled(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
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
	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
		runIDECommand: func(_ context.Context, params startTerminalSessionParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer app.shutdown(context.Background())

	if err := app.OpenIDE(uiSelection{Tenant: " erun ", Environment: " remote "}, "vscode"); err != nil {
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
		tenants: map[string]configpkg.TenantConfig{
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

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		runIDECommand: func(_ context.Context, params startTerminalSessionParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer app.shutdown(context.Background())

	if err := app.OpenIDE(uiSelection{Tenant: "erun", Environment: "local"}, "intellij"); err != nil {
		t.Fatalf("OpenIDE failed: %v", err)
	}
	wantExecutable, wantArgs, err := localOpenIDECommand(runtime.GOOS, "intellij", projectRoot)
	if err != nil {
		t.Fatalf("localOpenIDECommand failed: %v", err)
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

	executable, args, err := localOpenIDECommand("darwin", "vscode", projectRoot)
	if err != nil {
		t.Fatalf("localOpenIDECommand vscode failed: %v", err)
	}
	if executable != "open" || strings.Join(args, "\n") != strings.Join([]string{"-a", "Visual Studio Code", projectRoot}, "\n") {
		t.Fatalf("unexpected VS Code command: %s %+v", executable, args)
	}

	executable, args, err = localOpenIDECommand("darwin", "intellij", projectRoot)
	if err != nil {
		t.Fatalf("localOpenIDECommand intellij failed: %v", err)
	}
	if executable != "open" || strings.Join(args, "\n") != strings.Join([]string{"-a", "IntelliJ IDEA", projectRoot}, "\n") {
		t.Fatalf("unexpected IntelliJ command: %s %+v", executable, args)
	}
}

func TestOpenIDERejectsMissingSSHDWithoutHiddenInit(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]configpkg.TenantConfig{
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

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		runIDECommand: func(_ context.Context, params startTerminalSessionParams) (string, error) {
			started = params
			return "", nil
		},
	})
	defer app.shutdown(context.Background())

	err := app.OpenIDE(uiSelection{Tenant: "erun", Environment: "remote"}, "intellij")
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
