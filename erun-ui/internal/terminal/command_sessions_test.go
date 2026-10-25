package terminal

import (
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer closeTestService(app)

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
	if result.Kind != string(KindLocal) {
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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer closeTestService(app)

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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			startCalls++
			lastSession = newStubTerminalSession()
			return lastSession, nil
		},
	})
	defer closeTestService(app)

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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer closeTestService(app)

	result, err := app.StartSSHDInitSession(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartSSHDInitSession failed: %v", err)
	}
	if result.Kind != string(KindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	// Use the production builder: the shell prefix and line ending are per platform.
	want := BuildLocalErunCommand("/tmp/erun", []string{"sshd", "init", "erun", "remote"})
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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	defer closeTestService(app)

	result, err := app.StartDoctorSession(uimodel.Selection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDoctorSession failed: %v", err)
	}
	if result.Kind != string(KindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	want := BuildLocalErunCommand("/tmp/erun", []string{"doctor", "erun", "remote"})
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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			s := newStubTerminalSession()
			sessions = append(sessions, s)
			return s, nil
		},
	})
	// The inert auth-key seam leaves the in-shell deploy unauthenticated; main
	// pins the auth-injecting path.
	defer closeTestService(app)

	result, err := app.StartDeploySession(uimodel.Selection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if result.Kind != string(KindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	want := BuildLocalErunCommand("/tmp/erun", []string{"deploy", "erun", "remote", "--version", "1.0.19"})
	if !strings.Contains(written, want) {
		t.Fatalf("expected deploy command %q in Local pty, got %q", want, written)
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
	app := newTestService(Deps{
		Store:           store,
		FindProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		ResolveCLIPath:  func() string { return "/tmp/erun" },
		StartTerminal: func(StartParams) (PTY, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer closeTestService(app)

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
