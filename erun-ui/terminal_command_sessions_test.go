package main

import (
	"context"
	"os"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/mcpauth"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestStartInitSessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartInitSession(uimodel.Selection{
		Tenant:            " erun ",
		Environment:       " remote ",
		Version:           " 1.0.19 ",
		KubernetesContext: " orbstack ",
		ContainerRegistry: " erunpaas ",
		NoGit:             true,
		SetDefaultTenant:  true,
	}, 80, 24)
	if err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 spawned session (Local), got %d", len(sessions))
	}
	written := sessions[0].WrittenString()
	// Init appends --mcp-auth-public-key after --no-git.
	wantSubstr := "/tmp/erun init erun remote --type=remote-agent --version 1.0.19 --kubernetes-context orbstack --container-registry erunpaas --set-default-tenant=true --confirm-environment=true --no-git"
	if !strings.Contains(written, wantSubstr) {
		t.Fatalf("expected Local pty to receive %q, got %q", wantSubstr, written)
	}
}

func TestStartInitSessionUsesSeparateSessionKey(t *testing.T) {
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
			},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
}

func TestStartInitSessionReusesLocalAcrossInvocations(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	startCalls := 0
	var lastSession *stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			lastSession = newStubTerminalSession()
			return lastSession, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.18"}, 80, 24); err != nil {
		t.Fatalf("first StartInitSession failed: %v", err)
	}
	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("second StartInitSession failed: %v", err)
	}
	if startCalls != 1 {
		t.Fatalf("expected Local to be reused (1 spawn), got %d", startCalls)
	}
	written := lastSession.WrittenString()
	if !strings.Contains(written, "init erun remote --type=remote-agent --version 1.0.18") {
		t.Fatalf("expected first init command in Local, got %q", written)
	}
	if !strings.Contains(written, "init erun remote --type=remote-agent --version 1.0.19") {
		t.Fatalf("expected second init command in Local, got %q", written)
	}
}

func TestStartSSHDInitSessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartSSHDInitSession(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartSSHDInitSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	// Use the production builder: the shell prefix and line ending are per platform.
	want := buildLocalErunCommand("/tmp/erun", []string{"sshd", "init", "erun", "remote"})
	if !strings.Contains(written, want) {
		t.Fatalf("expected sshd-init command %q in Local pty, got %q", want, written)
	}
}

func TestStartDoctorSessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartDoctorSession(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDoctorSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	want := buildLocalErunCommand("/tmp/erun", []string{"doctor", "erun", "remote"})
	if !strings.Contains(written, want) {
		t.Fatalf("expected doctor command %q in Local pty, got %q", want, written)
	}
}

func TestStartDeploySessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	// With no desktop identity the in-shell deploy stays unauthenticated; the
	// auth-injecting path has its own test below.
	app.identity = nil
	defer app.shutdown(context.Background())

	result, err := app.StartDeploySession(uimodel.Selection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	want := buildLocalErunCommand("/tmp/erun", []string{"deploy", "erun", "remote", "--version", "1.0.19"})
	if !strings.Contains(written, want) {
		t.Fatalf("expected deploy command %q in Local pty, got %q", want, written)
	}
}

func TestStartDeploySessionInjectsMCPAuthPublicKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	identityDir := t.TempDir()
	app.identity = newDesktopIdentity(identityDir)
	defer app.shutdown(context.Background())

	if _, err := app.StartDeploySession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	wantPath := mcpauth.DesktopIdentityPublicKeyPath(identityDir)
	written := sessions[0].WrittenString()
	// Use the production builder: prefix, quoting and line ending are per platform.
	want := buildLocalErunCommand("/tmp/erun", []string{"deploy", "erun", "remote", "--version", "1.0.19", "--mcp-auth-public-key", wantPath})
	if !strings.Contains(written, want) {
		t.Fatalf("expected deploy command to carry %q, got %q", want, written)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected desktop public key written to %q: %v", wantPath, err)
	}
}

func TestStartInitSessionInjectsMCPAuthPublicKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	identityDir := t.TempDir()
	app.identity = newDesktopIdentity(identityDir)
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19", NoGit: true}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	wantPath := mcpauth.DesktopIdentityPublicKeyPath(identityDir)
	wantFlag := "--mcp-auth-public-key " + shellQuoteIfNeeded(wantPath)
	written := sessions[0].WrittenString()
	if !strings.Contains(written, wantFlag) {
		t.Fatalf("expected init command to carry %q, got %q", wantFlag, written)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected desktop public key written to %q: %v", wantPath, err)
	}
}

func TestRunErunCommandReusesLocalAndERunSpawnsSeparately(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]model.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]configpkg.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if _, err := app.StartDeploySession(uimodel.Selection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if _, err := app.StartSession(uimodel.Selection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("expected Local + ERun spawn (2 calls), got %d", startCalls)
	}
}
