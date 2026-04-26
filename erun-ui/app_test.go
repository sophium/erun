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
					{Name: "local", RuntimeVersion: "1.0.19-snapshot-20260418141901", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17000}},
					{Name: "remote", RuntimeVersion: "1.0.18", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17100}},
				},
			},
		},
	}, eruncommon.BuildInfo{Version: "1.0.50"})

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
	if state.Tenants[0].Environments[0].RuntimeVersion != "1.0.19-snapshot-20260418141901" {
		t.Fatalf("unexpected runtime version: %+v", state.Tenants[0].Environments[0])
	}
}

func TestStateFromListResultOmitsEmptyTenants(t *testing.T) {
	state := stateFromListResult(eruncommon.ListResult{
		Tenants: []eruncommon.ListTenantResult{
			{Name: "empty"},
			{
				Name: "active",
				Environments: []eruncommon.ListEnvironmentResult{
					{Name: "prod"},
				},
			},
		},
	}, eruncommon.BuildInfo{Version: "1.0.50"})

	if len(state.Tenants) != 1 || state.Tenants[0].Name != "active" {
		t.Fatalf("unexpected tenants: %+v", state.Tenants)
	}
}

func TestLoadStateUsesTenantSpecificDeployableVersionSuggestions(t *testing.T) {
	projectRoot := t.TempDir()
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]eruncommon.TenantConfig{
				"frs": {
					Name:               "frs",
					ProjectRoot:        projectRoot,
					DefaultEnvironment: "prod",
				},
			},
			envs: map[string]eruncommon.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-prod",
				},
			},
		},
		findProjectRoot:  func() (string, string, error) { return "frs", projectRoot, nil },
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "frs-devops":
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"},
					LatestStable:   "1.0.11",
					LatestSnapshot: "1.0.12-snapshot-20260414165809",
				}, nil
			case eruncommon.DefaultRuntimeImageName:
				return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{}, nil
		},
	})

	state, err := app.LoadState()
	if err != nil {
		t.Fatalf("LoadState failed: %v", err)
	}
	got := versionValues(state.VersionSuggestions)
	want := []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", state.VersionSuggestions, want)
	}
}

func TestLoadVersionSuggestionsFiltersOutMissingTenantImageTags(t *testing.T) {
	app := NewApp(erunUIDeps{
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "frs-devops":
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"},
					LatestStable:   "1.0.11",
					LatestSnapshot: "1.0.12-snapshot-20260414165809",
				}, nil
			case eruncommon.DefaultRuntimeImageName:
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.50", "1.0.49"},
					LatestStable:   "1.0.50",
					LatestSnapshot: "",
				}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{}, nil
		},
	})

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " frs "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	got := versionValues(suggestions)
	want := []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809", "1.0.50", "1.0.49"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
	if suggestions[0].Label != "frs latest stable" || suggestions[0].Image != "frs-devops" || suggestions[3].Label != "ERun current" || suggestions[3].Image != eruncommon.DefaultRuntimeImageName {
		t.Fatalf("unexpected suggestion metadata: %+v", suggestions)
	}
}

func TestLoadVersionSuggestionsDoesNotDuplicateDefaultRuntimeForErunTenant(t *testing.T) {
	var repositories []string
	app := NewApp(erunUIDeps{
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			repositories = append(repositories, repository)
			if repository != eruncommon.DefaultRuntimeImageName {
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{"1.0.48", "1.0.47", "1.0.50-snapshot-20260426090832"},
				LatestStable:   "1.0.48",
				LatestSnapshot: "1.0.50-snapshot-20260426090832",
			}, nil
		},
	})

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " erun "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	got := versionValues(suggestions)
	want := []string{"1.0.48", "1.0.47", "1.0.50-snapshot-20260426090832"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
	if strings.Join(repositories, "\n") != eruncommon.DefaultRuntimeImageName {
		t.Fatalf("expected one registry lookup for default runtime image, got %+v", repositories)
	}
}

func TestLoadVersionSuggestionsFallsBackToDefaultRuntimeTagsWhenTenantImageMissing(t *testing.T) {
	app := NewApp(erunUIDeps{
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "test-devops":
				return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			case eruncommon.DefaultRuntimeImageName:
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"},
					LatestStable:   "1.0.50",
					LatestSnapshot: "1.0.51-snapshot-20260414165809",
				}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{}, nil
		},
	})

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test ", Action: "deploy"})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	got := versionValues(suggestions)
	want := []string{"1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
}

func TestLoadVersionSuggestionsForInitUsesAvailableRuntimeImageTags(t *testing.T) {
	app := NewApp(erunUIDeps{
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			if repository == "test-devops" {
				return eruncommon.RuntimeRegistryVersions{
					Image:        namespace + "/" + repository,
					Tags:         []string{"1.0.48"},
					LatestStable: "1.0.48",
				}, nil
			}
			if repository != eruncommon.DefaultRuntimeImageName {
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{"1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"},
				LatestStable:   "1.0.50",
				LatestSnapshot: "1.0.51-snapshot-20260414165809",
			}, nil
		},
	})

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test ", Action: "init"})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	got := versionValues(suggestions)
	want := []string{"1.0.48", "1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
	if suggestions[0].Image != "test-devops" || suggestions[1].Image != eruncommon.DefaultRuntimeImageName {
		t.Fatalf("unexpected suggestion metadata: %+v", suggestions)
	}
}

func TestParseVersionOutputUsesLastVersionLine(t *testing.T) {
	info, ok := parseVersionOutput("trace line\nerun 1.0.50 (03ce970142a1 built 2026-04-24T17:38:53Z)\n")
	if !ok {
		t.Fatal("expected version output to parse")
	}
	if info.Version != "1.0.50" || info.Commit != "03ce970142a1" || info.Date != "2026-04-24T17:38:53Z" {
		t.Fatalf("unexpected build info: %+v", info)
	}
}

func versionValues(values []uiVersion) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value.Version)
	}
	return result
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

func TestBuildInitArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildInitArgs(" erun ", " remote ", "", "", false)
	want := []string{"init", "erun", "remote", "--remote"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsIncludesRuntimeVersion(t *testing.T) {
	got := buildInitArgs(" erun ", " remote ", " 1.0.19 ", " erun-devops ", true)
	want := []string{"init", "erun", "remote", "--remote", "--version", "1.0.19", "--runtime-image", "erun-devops", "--no-git"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildDeployArgsIncludesRuntimeVersion(t *testing.T) {
	got := buildDeployArgs(" erun ", " remote ", " 1.0.19 ", " erun-devops ")
	want := []string{"open", "erun", "remote", "--no-shell", "--no-alias-prompt", "--version", "1.0.19", "--runtime-image", "erun-devops"}
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

func TestStartInitSessionStartsRemoteInitCommand(t *testing.T) {
	projectRoot := t.TempDir()

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartInitSession(uiSelection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 ", NoGit: true}, 80, 24)
	if err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}

	if result.SessionID == 0 {
		t.Fatalf("expected session id, got %+v", result)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected start dir: %q", started.Dir)
	}
	if started.Executable != "/tmp/erun" {
		t.Fatalf("unexpected executable: %q", started.Executable)
	}
	wantArgs := []string{"init", "erun", "remote", "--remote", "--version", "1.0.19", "--no-git"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
}

func TestStartInitSessionUsesSeparateSessionKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
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

	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote"}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
}

func TestStartInitSessionUsesVersionInSessionKey(t *testing.T) {
	projectRoot := t.TempDir()
	startCalls := 0
	app := NewApp(erunUIDeps{
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.18"}, 80, 24); err != nil {
		t.Fatalf("first StartInitSession failed: %v", err)
	}
	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("second StartInitSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
}

func TestStartDeploySessionStartsDeployCommand(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartDeploySession(uiSelection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if result.SessionID == 0 {
		t.Fatalf("expected session id, got %+v", result)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected start dir: %q", started.Dir)
	}
	wantArgs := []string{"open", "erun", "remote", "--no-shell", "--no-alias-prompt", "--version", "1.0.19"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
}

func TestStartDeploySessionUsesLocalProjectRootForRemoteEnvironment(t *testing.T) {
	localRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        "/home/erun/git/frs",
				DefaultEnvironment: "prod",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				RepoPath:          "/home/erun/git/frs",
				KubernetesContext: "rancher-desktop",
				Remote:            true,
			},
		},
	}

	var started startTerminalSessionParams
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "frs", localRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartDeploySession(uiSelection{Tenant: "frs", Environment: "prod", Version: "1.0.50"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if started.Dir != localRoot {
		t.Fatalf("expected local deploy start dir %q, got %q", localRoot, started.Dir)
	}
}

func TestStartDeploySessionUsesSeparateSessionKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
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

	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if _, err := app.StartDeploySession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 3 {
		t.Fatalf("start terminal called %d times, want 3", startCalls)
	}
}

func TestDeleteEnvironmentRequiresExactConfirmationAndDeletesConfig(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "prod",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				RepoPath:          "/home/erun/git/frs",
				KubernetesContext: "cluster-prod",
				Remote:            true,
			},
		},
	}

	var deletedContext string
	var deletedNamespace string
	app := NewApp(erunUIDeps{
		store: store,
		deleteNamespace: func(contextName, namespace string) error {
			deletedContext = contextName
			deletedNamespace = namespace
			return nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.DeleteEnvironment(uiSelection{Tenant: "frs", Environment: "prod"}, "wrong"); err == nil {
		t.Fatal("expected confirmation mismatch")
	}
	if _, ok := store.envs["frs/prod"]; !ok {
		t.Fatal("expected env config to remain after failed confirmation")
	}

	result, err := app.DeleteEnvironment(uiSelection{Tenant: "frs", Environment: "prod"}, "frs-prod")
	if err != nil {
		t.Fatalf("DeleteEnvironment failed: %v", err)
	}
	if deletedContext != "cluster-prod" || deletedNamespace != "frs-prod" {
		t.Fatalf("unexpected namespace deletion: context=%q namespace=%q", deletedContext, deletedNamespace)
	}
	if result.Namespace != "frs-prod" || result.Tenant != "frs" || result.Environment != "prod" {
		t.Fatalf("unexpected delete result: %+v", result)
	}
	if _, ok := store.envs["frs/prod"]; ok {
		t.Fatal("expected env config to be deleted")
	}
	if store.tenants["frs"].DefaultEnvironment != "" {
		t.Fatalf("expected deleted default environment to be cleared, got %+v", store.tenants["frs"])
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
			return "/home/erun/.codex/attachments/paste.png", nil
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
	if result.Path != "/home/erun/.codex/attachments/paste.png" {
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

	remoteDir := pastedImageRemoteDir()
	if remoteDir != "/home/erun/.codex/attachments" {
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
		"mkdir -p '/home/erun/.codex/attachments' && base64 -d > '/home/erun/.codex/attachments/paste.png'",
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

func (s stubUIStore) SaveERunConfig(eruncommon.ERunConfig) error {
	return nil
}

func (s stubUIStore) LoadTenantConfig(name string) (eruncommon.TenantConfig, string, error) {
	config, ok := s.tenants[name]
	if !ok {
		return eruncommon.TenantConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) SaveTenantConfig(config eruncommon.TenantConfig) error {
	if s.tenants == nil {
		s.tenants = make(map[string]eruncommon.TenantConfig)
	}
	s.tenants[config.Name] = config
	return nil
}

func (s stubUIStore) DeleteTenantConfig(tenant string) error {
	delete(s.tenants, tenant)
	return nil
}

func (s stubUIStore) LoadEnvConfig(tenant, environment string) (eruncommon.EnvConfig, string, error) {
	config, ok := s.envs[tenant+"/"+environment]
	if !ok {
		return eruncommon.EnvConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
}

func (s stubUIStore) DeleteEnvConfig(tenant, environment string) error {
	delete(s.envs, tenant+"/"+environment)
	return nil
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
