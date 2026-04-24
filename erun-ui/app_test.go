package main

import (
	"context"
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

func TestStateFromListResultUsesEffectiveSelection(t *testing.T) {
	state := stateFromListResult(eruncommon.ListResult{
		CurrentDirectory: eruncommon.ListCurrentDirectoryResult{
			Effective: &eruncommon.ListEffectiveTargetResult{
				Tenant:      "erun",
				Environment: "local",
			},
		},
		Tenants: []eruncommon.ListTenantResult{
			{
				Name: "erun",
				Environments: []eruncommon.ListEnvironmentResult{
					{Name: "local", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17000}},
					{Name: "remote", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17100}},
				},
			},
		},
	})

	if state.Selected == nil {
		t.Fatal("expected selected environment")
	}
	if state.Selected.Tenant != "erun" || state.Selected.Environment != "local" {
		t.Fatalf("unexpected selected environment: %+v", state.Selected)
	}
	if len(state.Tenants) != 1 || len(state.Tenants[0].Environments) != 2 {
		t.Fatalf("unexpected tenants: %+v", state.Tenants)
	}
	if state.Tenants[0].Environments[0].MCPURL != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected MCP URL: %+v", state.Tenants[0].Environments[0])
	}
}

func TestLoadDiffUsesSelectedMCPPort(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}
	var gotEndpoint string
	app := NewApp(erunUIDeps{
		store: store,
		loadDiff: func(_ context.Context, endpoint string) (eruncommon.DiffResult, error) {
			gotEndpoint = endpoint
			return eruncommon.DiffResult{RawDiff: "diff --git a/a.txt b/a.txt\n"}, nil
		},
	})

	result, err := app.LoadDiff(uiSelection{Tenant: "erun", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadDiff failed: %v", err)
	}
	if gotEndpoint != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint: %q", gotEndpoint)
	}
	if result.RawDiff == "" {
		t.Fatalf("unexpected diff result: %+v", result)
	}
}

func TestBuildOpenCommandQuotesExecutableAndArgs(t *testing.T) {
	got := buildOpenCommand("/Applications/ERun App/erun", "tenant a", "prod")
	want := "'/Applications/ERun App/erun' open 'tenant a' 'prod'"
	if got != want {
		t.Fatalf("unexpected open command: got %q want %q", got, want)
	}
}

func TestBuildOpenArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildOpenArgs(" erun ", " local ")
	want := []string{"open", "erun", "local"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestResolveCLIExecutableFromDarwinBundleUsesSiblingCLI(t *testing.T) {
	root := t.TempDir()
	cliPath := filepath.Join(root, "erun")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	appExecutable := filepath.Join(root, "ERun.app", "Contents", "MacOS", "erun-app")
	got := resolveCLIExecutableFromPath("darwin", appExecutable, "erun")
	if got != cliPath {
		t.Fatalf("resolveCLIExecutableFromPath() = %q, want %q", got, cliPath)
	}
}

func TestResolveCLIExecutableFromPathUsesExecutableSibling(t *testing.T) {
	root := t.TempDir()
	cliPath := filepath.Join(root, "erun")
	if err := os.WriteFile(cliPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	got := resolveCLIExecutableFromPath("linux", filepath.Join(root, "erun-app"), "erun")
	if got != cliPath {
		t.Fatalf("resolveCLIExecutableFromPath() = %q, want %q", got, cliPath)
	}
}

func TestResolveTerminalStartDirUsesExistingPreferredDirectory(t *testing.T) {
	preferred := t.TempDir()

	if got := resolveTerminalStartDir(preferred); got != preferred {
		t.Fatalf("resolveTerminalStartDir(%q) = %q, want %q", preferred, got, preferred)
	}
}

func TestResolveTerminalStartDirFallsBackToWorkingDirectory(t *testing.T) {
	originalDir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd failed: %v", err)
	}

	workingDir := t.TempDir()
	if err := os.Chdir(workingDir); err != nil {
		t.Fatalf("Chdir failed: %v", err)
	}
	defer func() {
		if err := os.Chdir(originalDir); err != nil {
			t.Fatalf("restore working directory failed: %v", err)
		}
	}()

	missingDir := filepath.Join(workingDir, "missing")
	got := resolveTerminalStartDir(missingDir)
	gotEval, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", got, err)
	}
	wantEval, err := filepath.EvalSymlinks(workingDir)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q) failed: %v", workingDir, err)
	}
	if gotEval != wantEval {
		t.Fatalf("resolveTerminalStartDir(%q) = %q (%q), want %q (%q)", missingDir, got, gotEval, workingDir, wantEval)
	}
}

func TestStartSessionReusesExistingSessionForSelection(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	startCalls := 0
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", eruncommon.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}

	second, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}

	if startCalls != 1 {
		t.Fatalf("start terminal called %d times, want 1", startCalls)
	}
	if first.SessionID != second.SessionID {
		t.Fatalf("session ids differ: first=%d second=%d", first.SessionID, second.SessionID)
	}
}

func TestSavePastedImageCopiesIntoCurrentRuntime(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	imageData := []byte("png-data")
	var saved pastedImageSaveParams
	app := NewApp(erunUIDeps{
		store: store,
		savePastedImage: func(params pastedImageSaveParams) (string, error) {
			saved = params
			return "/home/erun/git/erun/.codex/attachments/paste.png", nil
		},
	})
	defer app.shutdown(context.Background())

	app.mu.Lock()
	app.current = &managedTerminal{
		session:   newStubTerminalSession(),
		selection: uiSelection{Tenant: "erun", Environment: "local"},
	}
	app.mu.Unlock()

	result, err := app.SavePastedImage(pastedImagePayload{
		Data:     base64.StdEncoding.EncodeToString(imageData),
		MIMEType: "image/png",
		Name:     "screenshot.png",
	})
	if err != nil {
		t.Fatalf("SavePastedImage failed: %v", err)
	}
	if result.Path != "/home/erun/git/erun/.codex/attachments/paste.png" {
		t.Fatalf("unexpected pasted image path: %q", result.Path)
	}
	if string(saved.Data) != string(imageData) {
		t.Fatalf("unexpected saved data: %q", string(saved.Data))
	}
	if saved.MIMEType != "image/png" || saved.Name != "screenshot.png" {
		t.Fatalf("unexpected saved metadata: %+v", saved)
	}
	if saved.Result.Tenant != "erun" || saved.Result.Environment != "local" {
		t.Fatalf("unexpected resolved target: %+v", saved.Result)
	}
}

func TestBeforeClosePersistsMaximisedWindowState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "window-state.json")
	app := NewApp(erunUIDeps{
		windowStatePath: statePath,
		windowMaximised: func(context.Context) bool {
			return true
		},
	})

	if prevent := app.beforeClose(context.Background()); prevent {
		t.Fatal("beforeClose should not prevent shutdown")
	}

	state := loadAppWindowState(statePath)
	if !state.Maximised {
		t.Fatalf("expected maximised state to be persisted: %+v", state)
	}
}

func TestSaveAndLoadAppWindowState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "nested", "window-state.json")

	if err := saveAppWindowState(statePath, appWindowState{Maximised: true}); err != nil {
		t.Fatalf("saveAppWindowState failed: %v", err)
	}

	state := loadAppWindowState(statePath)
	if !state.Maximised {
		t.Fatalf("unexpected loaded window state: %+v", state)
	}
}

func TestDecodePastedImagePayloadAcceptsDataURL(t *testing.T) {
	imageData := []byte("png-data")
	got, mimeType, err := decodePastedImagePayload(pastedImagePayload{
		Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData),
	})
	if err != nil {
		t.Fatalf("decodePastedImagePayload failed: %v", err)
	}
	if string(got) != string(imageData) {
		t.Fatalf("unexpected decoded data: %q", string(got))
	}
	if mimeType != "image/png" {
		t.Fatalf("unexpected mime type: %q", mimeType)
	}
}

func TestBuildPastedImageCopyCommandTargetsRuntimeDeployment(t *testing.T) {
	result := eruncommon.OpenResult{
		Tenant:      "erun",
		Environment: "local",
		RepoPath:    "/Users/example/git/erun",
		EnvConfig: eruncommon.EnvConfig{
			KubernetesContext: "rancher-desktop",
		},
	}

	remoteDir := pastedImageRemoteDir(result)
	if remoteDir != "/home/erun/git/erun/.codex/attachments" {
		t.Fatalf("unexpected remote dir: %q", remoteDir)
	}

	name, args, script := buildPastedImageCopyCommand(result, remoteDir, remoteDir+"/paste.png")
	if name != "kubectl" {
		t.Fatalf("unexpected command name: %q", name)
	}
	wantArgs := []string{
		"--context", "rancher-desktop",
		"--namespace", "erun-local",
		"exec", "-i",
		"-c", "erun-devops",
		"deployment/erun-devops",
		"--",
		"/bin/sh", "-lc",
		"mkdir -p '/home/erun/git/erun/.codex/attachments' && base64 -d > '/home/erun/git/erun/.codex/attachments/paste.png'",
	}
	if strings.Join(args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args:\n%q\nwant:\n%q", args, wantArgs)
	}
	if script != wantArgs[len(wantArgs)-1] {
		t.Fatalf("unexpected script: %q", script)
	}
}

type stubUIStore struct {
	tenants map[string]eruncommon.TenantConfig
	envs    map[string]eruncommon.EnvConfig
}

func (s stubUIStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	return eruncommon.ERunConfig{}, "", nil
}

func (s stubUIStore) LoadTenantConfig(name string) (eruncommon.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return eruncommon.TenantConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) LoadEnvConfig(tenant, environment string) (eruncommon.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return eruncommon.EnvConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) ListTenantConfigs() ([]eruncommon.TenantConfig, error) {
	tenants := make([]eruncommon.TenantConfig, 0, len(s.tenants))
	for _, tenant := range s.tenants {
		tenants = append(tenants, tenant)
	}
	return tenants, nil
}

func (s stubUIStore) ListEnvConfigs(tenant string) ([]eruncommon.EnvConfig, error) {
	envs := make([]eruncommon.EnvConfig, 0)
	for key, env := range s.envs {
		if strings.HasPrefix(key, tenant+"/") {
			envs = append(envs, env)
		}
	}
	return envs, nil
}

type stubTerminalSession struct {
	closeCh chan struct{}
}

func newStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{closeCh: make(chan struct{})}
}

func (s *stubTerminalSession) Read([]byte) (int, error) {
	<-s.closeCh
	return 0, io.EOF
}

func (s *stubTerminalSession) Write(buffer []byte) (int, error) {
	return len(buffer), nil
}

func (s *stubTerminalSession) Resize(int, int) error {
	return nil
}

func (s *stubTerminalSession) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}
