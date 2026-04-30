package main

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

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
					{Name: "local", RuntimeVersion: "1.0.19-snapshot-20260418141901", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17000}, SSH: eruncommon.ListSSHResult{Enabled: true}},
					{Name: "remote", RuntimeVersion: "1.0.18", Remote: true, LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17100}},
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
	if !state.Tenants[0].Environments[0].SSHDEnabled || state.Tenants[0].Environments[1].SSHDEnabled {
		t.Fatalf("unexpected SSHD flags: %+v", state.Tenants[0].Environments)
	}
	if state.Tenants[0].Environments[0].Remote || !state.Tenants[0].Environments[1].Remote {
		t.Fatalf("unexpected remote flags: %+v", state.Tenants[0].Environments)
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
		resolveBuildInfo:     func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: missingTenantImageRegistry(t),
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

func missingTenantImageRegistry(t *testing.T) func(context.Context, string, string) (eruncommon.RuntimeRegistryVersions, error) {
	t.Helper()

	return func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
		if namespace != eruncommon.DefaultContainerRegistry {
			t.Fatalf("unexpected registry namespace: %s", namespace)
		}
		if repository == "frs-devops" {
			return eruncommon.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"},
				LatestStable:   "1.0.11",
				LatestSnapshot: "1.0.12-snapshot-20260414165809",
			}, nil
		}
		if repository == eruncommon.DefaultRuntimeImageName {
			return eruncommon.RuntimeRegistryVersions{
				Image:        namespace + "/" + repository,
				Tags:         []string{"1.0.50", "1.0.49"},
				LatestStable: "1.0.50",
			}, nil
		}
		t.Fatalf("unexpected registry repository: %s", repository)
		return eruncommon.RuntimeRegistryVersions{}, nil
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

func TestLoadKubernetesContextsNormalizesContexts(t *testing.T) {
	app := NewApp(erunUIDeps{
		listKubeContexts: func() ([]string, error) {
			return []string{" cluster-b ", "cluster-a", "cluster-b", ""}, nil
		},
	})
	defer app.shutdown(context.Background())

	contexts, err := app.LoadKubernetesContexts()
	if err != nil {
		t.Fatalf("LoadKubernetesContexts failed: %v", err)
	}

	want := []string{"cluster-b", "cluster-a"}
	if strings.Join(contexts, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected contexts: got %+v want %+v", contexts, want)
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
	var ensured eruncommon.OpenResult
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return false
		},
		ensureMCP: func(_ context.Context, result eruncommon.OpenResult) error {
			ensured = result
			return nil
		},
		loadDiff: func(_ context.Context, endpoint string, options uiDiffOptions) (eruncommon.DiffResult, error) {
			gotEndpoint = endpoint
			if options.Scope != "commit" || options.SelectedCommit != "abc123" {
				t.Fatalf("unexpected diff options: %+v", options)
			}
			return eruncommon.DiffResult{RawDiff: "diff --git a/a.txt b/a.txt\n"}, nil
		},
	})

	result, err := app.LoadDiff(uiSelection{Tenant: "erun", Environment: "local"}, uiDiffOptions{Scope: " commit ", SelectedCommit: " abc123 "})
	if err != nil {
		t.Fatalf("LoadDiff failed: %v", err)
	}
	if gotEndpoint != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected endpoint: %q", gotEndpoint)
	}
	if ensured.Tenant != "erun" || ensured.Environment != "local" {
		t.Fatalf("expected MCP forward to be ensured before diff, got %+v", ensured)
	}
	if result.RawDiff == "" {
		t.Fatalf("unexpected diff result: %+v", result)
	}
}

func TestLoadDiffReactivatesMCPAfterConnectionError(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/test": {
				Name:              "test",
				RepoPath:          projectRoot,
				KubernetesContext: "orbstack",
			},
		},
	}
	ensureCalls := 0
	loadCalls := 0
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		ensureMCP: func(_ context.Context, result eruncommon.OpenResult) error {
			ensureCalls++
			if result.Tenant != "erun" || result.Environment != "test" {
				t.Fatalf("unexpected MCP target: %+v", result)
			}
			return nil
		},
		loadDiff: func(_ context.Context, endpoint string, _ uiDiffOptions) (eruncommon.DiffResult, error) {
			loadCalls++
			if endpoint != "http://127.0.0.1:17000/mcp" {
				t.Fatalf("unexpected endpoint: %q", endpoint)
			}
			if loadCalls == 1 {
				return eruncommon.DiffResult{}, errors.New("EOF")
			}
			return eruncommon.DiffResult{RawDiff: "diff --git a/a.txt b/a.txt\n"}, nil
		},
	})

	result, err := app.LoadDiff(uiSelection{Tenant: "erun", Environment: "test"}, uiDiffOptions{})
	if err != nil {
		t.Fatalf("LoadDiff failed: %v", err)
	}
	if ensureCalls != 1 || loadCalls != 2 {
		t.Fatalf("expected one MCP reactivation and retry, got ensure=%d load=%d", ensureCalls, loadCalls)
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

func TestBuildOpenArgsIncludesDebugVerbosity(t *testing.T) {
	got := buildOpenArgs(" erun ", " local ", true)
	want := []string{"-vv", "open", "erun", "local"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", got, want)
	}
}

func TestBuildOpenIDEArgsAddsIDEFlag(t *testing.T) {
	got := buildOpenIDEArgs(uiSelection{Tenant: " erun ", Environment: " remote ", Debug: true}, "vscode")
	want := []string{"-vv", "open", "erun", "remote", "--vscode"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected VS Code args: got %+v want %+v", got, want)
	}

	got = buildOpenIDEArgs(uiSelection{Tenant: "erun", Environment: "remote"}, "intellij")
	want = []string{"open", "erun", "remote", "--intellij"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected IntelliJ args: got %+v want %+v", got, want)
	}
}

func TestBuildDoctorArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildDoctorArgs(uiSelection{Tenant: " erun ", Environment: " remote ", Debug: true})
	want := []string{"-vv", "doctor", "erun", "remote"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected doctor args: got %+v want %+v", got, want)
	}
}

func TestBuildOpenNoShellArgsTrimsTenantAndEnvironment(t *testing.T) {
	got := buildOpenNoShellArgs(" erun ", " local ")
	want := []string{"open", "erun", "local", "--no-shell", "--no-alias-prompt"}
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
	got := buildInitArgs(uiSelection{Tenant: " erun ", Environment: " remote "})
	want := []string{"init", "erun", "remote", "--remote", "--set-default-tenant=false", "--confirm-environment=true"}
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
	got := buildInitArgs(uiSelection{
		Tenant:            " erun ",
		Environment:       " remote ",
		Version:           " 1.0.19 ",
		RuntimeImage:      " erun-devops ",
		RuntimeCPU:        " 6 ",
		RuntimeMemory:     " 12Gi ",
		KubernetesContext: " orbstack ",
		ContainerRegistry: " erunpaas ",
		NoGit:             true,
		Bootstrap:         true,
		SetDefaultTenant:  true,
	})
	want := []string{"init", "erun", "remote", "--remote", "--version", "1.0.19", "--runtime-image", "erun-devops", "--runtime-cpu", "6", "--runtime-memory", "12Gi", "--kubernetes-context", "orbstack", "--container-registry", "erunpaas", "--set-default-tenant=true", "--confirm-environment=true", "--no-git", "--bootstrap"}
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
	got := buildDeployArgs(uiSelection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 ", RuntimeImage: " erun-devops "})
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

	result, err := app.StartInitSession(uiSelection{
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

	if result.SessionID == 0 {
		t.Fatalf("expected session id, got %+v", result)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected start dir: %q", started.Dir)
	}
	if started.Executable != "/tmp/erun" {
		t.Fatalf("unexpected executable: %q", started.Executable)
	}
	wantArgs := []string{"init", "erun", "remote", "--remote", "--version", "1.0.19", "--kubernetes-context", "orbstack", "--container-registry", "erunpaas", "--set-default-tenant=true", "--confirm-environment=true", "--no-git"}
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

func TestStartSSHDInitSessionStartsSSHDInitCommand(t *testing.T) {
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
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartSSHDInitSession(uiSelection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartSSHDInitSession failed: %v", err)
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
	wantArgs := []string{"sshd", "init", "erun", "remote"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
}

func TestStartDoctorSessionStartsDoctorCommand(t *testing.T) {
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
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartDoctorSession(uiSelection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDoctorSession failed: %v", err)
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
	wantArgs := []string{"doctor", "erun", "remote"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
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

func TestOpenIDERunsWithoutConsumingTerminalWhenSSHDEnabled(t *testing.T) {
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
				Remote:            true,
				SSHD: eruncommon.SSHDConfig{
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
				Remote:            true,
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
	assertDeletedEnvironment(t, store, result, deletedContext, deletedNamespace)
}

func assertDeletedEnvironment(t *testing.T, store stubUIStore, result deleteEnvironmentResult, deletedContext, deletedNamespace string) {
	t.Helper()

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

func TestLoadAndSaveTenantConfig(t *testing.T) {
	snapshot := false
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        "/tmp/old",
				DefaultEnvironment: "dev",
				Remote:             true,
				Snapshot:           &snapshot,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadTenantConfig(" frs ")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if loaded.Name != "frs" || loaded.DefaultEnvironment != "dev" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}

	saved, err := app.SaveTenantConfig(uiTenantConfig{
		Name:               "frs",
		DefaultEnvironment: " prod ",
	})
	if err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if saved.DefaultEnvironment != "prod" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if store.tenants["frs"].ProjectRoot != "/tmp/old" || !store.tenants["frs"].Remote || store.tenants["frs"].Snapshot == nil || *store.tenants["frs"].Snapshot {
		t.Fatalf("expected tenant project root/remote/snapshot to be preserved, got %+v", store.tenants["frs"])
	}
}

func TestLoadAndSaveERunConfig(t *testing.T) {
	config := eruncommon.ERunConfig{DefaultTenant: "old-tenant"}
	store := stubUIStore{
		config: &config,
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadERunConfig()
	if err != nil {
		t.Fatalf("LoadERunConfig failed: %v", err)
	}
	if loaded.DefaultTenant != "old-tenant" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}

	saved, err := app.SaveERunConfig(uiERunConfig{DefaultTenant: " new-tenant "})
	if err != nil {
		t.Fatalf("SaveERunConfig failed: %v", err)
	}
	if saved.DefaultTenant != "new-tenant" || config.DefaultTenant != "new-tenant" {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, config)
	}
}

func TestLoadAndSaveEnvironmentConfig(t *testing.T) {
	projectRoot := t.TempDir()
	snapshot := true
	rootConfig := &eruncommon.ERunConfig{
		CloudProviders: []eruncommon.CloudProviderConfig{
			{Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS},
		},
		CloudContexts: []eruncommon.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           eruncommon.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             eruncommon.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       eruncommon.DefaultCloudContextInstanceType,
				DiskType:           eruncommon.DefaultCloudContextDiskType,
				DiskSizeGB:         eruncommon.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-old",
				Status:             eruncommon.CloudContextStatusStopped,
			},
		},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:        "frs",
				ProjectRoot: projectRoot,
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:               "prod",
				RepoPath:           projectRoot,
				KubernetesContext:  "cluster-old",
				ContainerRegistry:  "registry.example/old",
				CloudProviderAlias: "team-cloud",
				RuntimeVersion:     "1.0.0",
				RuntimePod: eruncommon.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
				SSHD: eruncommon.SSHDConfig{
					Enabled:       false,
					LocalPort:     60022,
					PublicKeyPath: "/tmp/old.pub",
				},
				Remote:   false,
				Snapshot: &snapshot,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadEnvironmentConfig(uiSelection{Tenant: " frs ", Environment: " prod "})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	assertLoadedEnvironmentConfig(t, loaded, projectRoot)

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name:               "prod",
		RepoPath:           " /tmp/repo ",
		KubernetesContext:  " cluster-new ",
		ContainerRegistry:  " registry.example/team ",
		CloudProviderAlias: " other-cloud ",
		RuntimeVersion:     " 1.2.3 ",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "6",
			Memory: "12Gi",
		},
		SSHD: uiSSHDConfig{
			Enabled:       true,
			LocalPort:     62222,
			PublicKeyPath: " /tmp/id_ed25519.pub ",
		},
		Remote:   true,
		Snapshot: false,
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	assertSavedEnvironmentConfig(t, saved, projectRoot)
	stored := store.envs["frs/prod"]
	assertStoredEnvironmentConfig(t, stored, projectRoot)
}

func assertLoadedEnvironmentConfig(t *testing.T, loaded uiEnvironmentConfig, projectRoot string) {
	t.Helper()

	if loaded.Name != "prod" || loaded.RepoPath != projectRoot || loaded.KubernetesContext != "cluster-old" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if loaded.CloudContext == nil || loaded.CloudContext.Name != "team-context" || loaded.CloudContext.Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected linked cloud context, got %+v", loaded.CloudContext)
	}
	if loaded.RuntimePod.CPU != "4" || loaded.RuntimePod.Memory != "8916Mi" {
		t.Fatalf("unexpected loaded runtime pod config: %+v", loaded.RuntimePod)
	}
	assertLocalPorts(t, loaded.LocalPorts)
}

func assertSavedEnvironmentConfig(t *testing.T, saved uiEnvironmentConfig, projectRoot string) {
	t.Helper()

	if saved.RepoPath != projectRoot || saved.KubernetesContext != "cluster-old" || saved.ContainerRegistry != "registry.example/old" || saved.RuntimeVersion != "1.0.0" || saved.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if saved.RuntimePod.CPU != "6" || saved.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected saved runtime pod config: %+v", saved.RuntimePod)
	}
	assertLocalPorts(t, saved.LocalPorts)
}

func assertLocalPorts(t *testing.T, ports uiEnvironmentLocalPorts) {
	t.Helper()

	if ports.RangeStart != 17000 || ports.RangeEnd != 17099 || ports.MCP != 17000 || ports.SSH != 60022 {
		t.Fatalf("unexpected local ports: %+v", ports)
	}
}

func assertStoredEnvironmentConfig(t *testing.T, stored eruncommon.EnvConfig, projectRoot string) {
	t.Helper()

	if stored.RepoPath != projectRoot || stored.Remote || stored.RuntimeVersion != "1.0.0" || stored.CloudProviderAlias != "other-cloud" || stored.SSHD.Enabled || stored.SSHD.LocalPort != 60022 || stored.SSHD.PublicKeyPath != "/tmp/old.pub" || stored.Snapshot == nil || *stored.Snapshot {
		t.Fatalf("unexpected stored config: %+v", stored)
	}
	if stored.RuntimePod.CPU != "6" || stored.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected stored runtime pod config: %+v", stored.RuntimePod)
	}
}

func TestSaveRemoteEnvironmentConfigSetsCloudAliasViaMCP(t *testing.T) {
	projectRoot := eruncommon.RemoteWorktreePathForRepoName("frs")
	rootConfig := &eruncommon.ERunConfig{
		CloudContexts: []eruncommon.CloudContextConfig{{
			Name:               "team-context",
			CloudProviderAlias: "team-cloud",
			KubernetesContext:  "cluster-dev",
		}},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:               "frs",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "dev",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				RepoPath:           projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
				Remote:             true,
			},
		},
	}
	var remoteEndpoint string
	var remoteTenant string
	var remoteEnvironment string
	var remoteAlias string
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		setRemoteCloudAlias: func(_ context.Context, endpoint, tenant, environment, alias string) (eruncommon.EnvConfig, error) {
			remoteEndpoint = endpoint
			remoteTenant = tenant
			remoteEnvironment = environment
			remoteAlias = alias
			return eruncommon.EnvConfig{Name: environment, CloudProviderAlias: alias}, nil
		},
	})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "dev"}, uiEnvironmentConfig{
		Name:               "dev",
		CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if saved.CloudProviderAlias != "team-cloud" || store.envs["frs/dev"].CloudProviderAlias != "team-cloud" || !store.envs["frs/dev"].ManagedCloud {
		t.Fatalf("unexpected saved config: result=%+v stored=%+v", saved, store.envs["frs/dev"])
	}
	if remoteEndpoint != "http://127.0.0.1:17000/mcp" || remoteTenant != "frs" || remoteEnvironment != "dev" || remoteAlias != "team-cloud" {
		t.Fatalf("unexpected remote alias call: endpoint=%q tenant=%q environment=%q alias=%q", remoteEndpoint, remoteTenant, remoteEnvironment, remoteAlias)
	}
}

func TestStartSessionLeavesCloudContextStartupToErunCommand(t *testing.T) {
	projectRoot := t.TempDir()
	rootConfig := &eruncommon.ERunConfig{
		CloudProviders: []eruncommon.CloudProviderConfig{
			{Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS},
		},
		CloudContexts: []eruncommon.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           eruncommon.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             eruncommon.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       eruncommon.DefaultCloudContextInstanceType,
				DiskType:           eruncommon.DefaultCloudContextDiskType,
				DiskSizeGB:         eruncommon.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-prod",
				AdminToken:         "test-token",
				Status:             eruncommon.CloudContextStatusStopped,
			},
		},
	}
	var actions []string
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			config: rootConfig,
			tenants: map[string]eruncommon.TenantConfig{
				"frs": {Name: "frs", ProjectRoot: projectRoot, DefaultEnvironment: "prod"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					RepoPath:          projectRoot,
					KubernetesContext: "cluster-prod",
					Remote:            true,
				},
			},
		},
		resolveCLIPath:   func() string { return "/tmp/erun" },
		cloudContextDeps: testCloudContextDeps(&actions),
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			actions = append(actions, "terminal "+strings.Join(params.Args, " "))
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "frs", Environment: "prod"}, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	got := strings.Join(actions, "\n")
	if got != "terminal open frs prod" {
		t.Fatalf("expected only terminal start action, got:\n%s", got)
	}
	if rootConfig.CloudContexts[0].Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected cloud context startup to be left to erun, got %+v", rootConfig.CloudContexts[0])
	}
}

func TestDeleteEnvironmentStartsLinkedContextThenStopsIt(t *testing.T) {
	projectRoot := t.TempDir()
	rootConfig := &eruncommon.ERunConfig{
		DefaultTenant: "frs",
		CloudProviders: []eruncommon.CloudProviderConfig{
			{Alias: "team-cloud", Provider: eruncommon.CloudProviderAWS},
		},
		CloudContexts: []eruncommon.CloudContextConfig{
			{
				Name:               "team-context",
				Provider:           eruncommon.CloudProviderAWS,
				CloudProviderAlias: "team-cloud",
				Region:             eruncommon.DefaultCloudContextRegion,
				InstanceID:         "i-test",
				InstanceType:       eruncommon.DefaultCloudContextInstanceType,
				DiskType:           eruncommon.DefaultCloudContextDiskType,
				DiskSizeGB:         eruncommon.DefaultCloudContextDiskSizeGB,
				KubernetesContext:  "cluster-prod",
				AdminToken:         "test-token",
				Status:             eruncommon.CloudContextStatusStopped,
			},
		},
	}
	store := stubUIStore{
		config: rootConfig,
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", ProjectRoot: projectRoot, DefaultEnvironment: "prod"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-prod",
				Remote:            true,
			},
		},
	}
	var actions []string
	app := NewApp(erunUIDeps{
		store:            store,
		cloudContextDeps: testCloudContextDeps(&actions),
		deleteNamespace: func(context, namespace string) error {
			actions = append(actions, "delete-namespace "+context+" "+namespace)
			return nil
		},
	})

	result, err := app.DeleteEnvironment(uiSelection{Tenant: "frs", Environment: "prod"}, "frs-prod")
	if err != nil {
		t.Fatalf("DeleteEnvironment failed: %v", err)
	}
	if result.CloudContextStopError != "" {
		t.Fatalf("unexpected cloud context stop error: %+v", result)
	}
	got := strings.Join(actions, "\n")
	for _, want := range []string{
		"aws ec2 start-instances --instance-ids i-test",
		"delete-namespace cluster-prod frs-prod",
		"aws ec2 stop-instances --instance-ids i-test",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected action %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, "aws ec2 start-instances") > strings.Index(got, "delete-namespace") || strings.Index(got, "delete-namespace") > strings.Index(got, "aws ec2 stop-instances") {
		t.Fatalf("unexpected delete ordering:\n%s", got)
	}
	if _, _, err := store.LoadEnvConfig("frs", "prod"); !errors.Is(err, eruncommon.ErrNotInitialized) {
		t.Fatalf("expected environment config to be deleted, got %v", err)
	}
	if rootConfig.CloudContexts[0].Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected cloud context to be stopped, got %+v", rootConfig.CloudContexts[0])
	}
}

func TestLocalPortStatusReportsAvailability(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on ephemeral port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port

	connectable := localPortStatus(port)
	if !connectable.Available || connectable.Status != "Yes" {
		t.Fatalf("expected connectable status, got %+v", connectable)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	unreachable := localPortStatus(port)
	if unreachable.Available || unreachable.Status != "No" {
		t.Fatalf("expected unreachable status, got %+v", unreachable)
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

func TestSendSessionInputRecordsCLIActivityForCurrentEnvironment(t *testing.T) {
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

	var recorded []eruncommon.EnvironmentActivityParams
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		recordActivity: func(params eruncommon.EnvironmentActivityParams) error {
			recorded = append(recorded, params)
			return nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if err := app.SendSessionInput("date\r"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}

	if len(recorded) != 2 {
		t.Fatalf("recorded %d activity events, want 2", len(recorded))
	}
	for _, event := range recorded {
		if event.Tenant != "erun" || event.Environment != "local" || event.Kind != eruncommon.ActivityKindCLI {
			t.Fatalf("unexpected activity params: %+v", event)
		}
	}
}

func TestMergeNewerActivityMarkersPrefersLocalTerminalActivity(t *testing.T) {
	now := time.Now()
	remote := eruncommon.EnvironmentIdleStatus{
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: "working-hours", LastActivity: now},
			{Name: eruncommon.ActivityKindCLI, LastActivity: now.Add(-4 * time.Minute), SecondsRemaining: 60},
			{Name: eruncommon.ActivityKindMCP, LastActivity: now.Add(-1 * time.Minute), SecondsRemaining: 240},
		},
	}
	local := eruncommon.EnvironmentIdleStatus{
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: eruncommon.ActivityKindCLI, LastActivity: now, SecondsRemaining: 300},
			{Name: eruncommon.ActivityKindMCP, LastActivity: now.Add(-2 * time.Minute), SecondsRemaining: 180},
		},
	}

	merged := mergeNewerActivityMarkers(remote, local)
	if got := activitySecondsUntilIdle(merged); got != 300 {
		t.Fatalf("activitySecondsUntilIdle = %d, want 300", got)
	}
	for _, marker := range merged.Markers {
		if marker.Name == eruncommon.ActivityKindMCP && marker.SecondsRemaining != 240 {
			t.Fatalf("older local MCP marker should not replace remote marker: %+v", marker)
		}
	}
}

func TestMergeNewerActivityMarkersAddsMissingLocalTerminalActivity(t *testing.T) {
	now := time.Now()
	remote := eruncommon.EnvironmentIdleStatus{
		ManagedCloud: true,
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: "working-hours", LastActivity: now},
			{Name: eruncommon.ActivityKindMCP, Idle: true, LastActivity: now.Add(-10 * time.Minute), SecondsRemaining: 0},
		},
	}
	local := eruncommon.EnvironmentIdleStatus{
		ManagedCloud: true,
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: eruncommon.ActivityKindCLI, Idle: false, LastActivity: now, SecondsRemaining: 60},
		},
	}

	merged := mergeNewerActivityMarkers(remote, local)
	if merged.StopEligible {
		t.Fatal("expected local CLI activity to block idle stop")
	}
	if got := activitySecondsUntilIdle(merged); got != 60 {
		t.Fatalf("activitySecondsUntilIdle = %d, want 60", got)
	}
	if merged.StopBlockedReason != eruncommon.ActivityKindCLI {
		t.Fatalf("StopBlockedReason = %q, want %q", merged.StopBlockedReason, eruncommon.ActivityKindCLI)
	}
}

func TestMergeLocalIdleActivityUsesSavedPolicyWithRemoteActivity(t *testing.T) {
	now := time.Now()
	store := stubUIStore{
		envs: map[string]eruncommon.EnvConfig{
			"team-stop/dev-stop": {
				Name: "dev-stop",
				Idle: eruncommon.EnvironmentIdleConfig{
					Timeout:      "10s",
					WorkingHours: "00:00-23:59",
				},
				ManagedCloud: true,
				Remote:       true,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	remote := eruncommon.EnvironmentIdleStatus{
		Policy: eruncommon.EnvironmentIdlePolicy{
			Timeout:      5 * time.Minute,
			WorkingHours: "00:00-23:59",
		},
		ManagedCloud: true,
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: eruncommon.ActivityKindCLI, Idle: false, SecondsRemaining: 295},
		},
		Activity: map[string]eruncommon.EnvironmentActivitySnapshot{
			eruncommon.ActivityKindCLI: {LastActivity: now.Add(-5 * time.Second), LastSeen: now.Add(-5 * time.Second)},
		},
	}

	merged := app.mergeLocalIdleActivity(eruncommon.OpenResult{
		Tenant:      "team-stop",
		Environment: "dev-stop",
		EnvConfig:   store.envs["team-stop/dev-stop"],
	}, remote)

	if merged.Policy.Timeout != 10*time.Second {
		t.Fatalf("expected saved local timeout, got %s", merged.Policy.Timeout)
	}
	if got := activitySecondsUntilIdle(merged); got <= 0 || got > 10 {
		t.Fatalf("activitySecondsUntilIdle = %d, want local 10s policy countdown", got)
	}
}

func TestLoadIdleStatusStopsLinkedCloudContextWhenStopEligible(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
				Status:             eruncommon.CloudContextStatusRunning,
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"team-stop": {
				Name:               "team-stop",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "dev-stop",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"team-stop/dev-stop": {
				Name:               "dev-stop",
				RepoPath:           projectRoot,
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Remote:             true,
			},
		},
	}
	stopped := make(chan string, 1)
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return true },
		loadIdleStatus: func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error) {
			return eruncommon.EnvironmentIdleStatus{
				ManagedCloud: true,
				StopEligible: true,
				Policy: eruncommon.EnvironmentIdlePolicy{
					Timeout: 5 * time.Minute,
				},
				Markers: []eruncommon.EnvironmentIdleMarker{
					{Name: "working-hours", Idle: true},
					{Name: eruncommon.ActivityKindSSH, Idle: true},
					{Name: eruncommon.ActivityKindMCP, Idle: true},
					{Name: eruncommon.ActivityKindCLI, Idle: true},
					{Name: eruncommon.ActivityKindCodex, Idle: true},
				},
			}, nil
		},
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			stopped <- name
			return eruncommon.CloudContextStatus{}, nil
		},
	})
	defer app.shutdown(context.Background())

	status, err := app.LoadIdleStatus(uiSelection{Tenant: "team-stop", Environment: "dev-stop"})
	if err != nil {
		t.Fatalf("LoadIdleStatus failed: %v", err)
	}
	if status.CloudContextName != "cloud-ctx" || status.CloudContextStatus != eruncommon.CloudContextStatusRunning || status.CloudContextLabel != "cluster-cloud" {
		t.Fatalf("unexpected linked cloud context details: %+v", status)
	}

	select {
	case got := <-stopped:
		if got != "cloud-ctx" {
			t.Fatalf("stopped context %q, want cloud-ctx", got)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for cloud context stop")
	}
}

func TestStartCloudContextClearsPreviousIdleStop(t *testing.T) {
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
				Status:             eruncommon.CloudContextStatusRunning,
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"team-stop": {
				Name:               "team-stop",
				DefaultEnvironment: "dev-stop",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"team-stop/dev-stop": {
				Name:               "dev-stop",
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Remote:             true,
			},
		},
	}
	key := selectionKey(uiSelection{Tenant: "team-stop", Environment: "dev-stop"})
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())

	app.idleStops[key] = struct{}{}
	app.clearIdleStopsForCloudContext("cloud-ctx")

	if _, exists := app.idleStops[key]; exists {
		t.Fatal("expected previous idle stop marker to be cleared")
	}
}

func TestLoadIdleStatusDoesNotStopWhileEnvironmentCommandRunning(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
				Status:             eruncommon.CloudContextStatusRunning,
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"team-busy": {
				Name:               "team-busy",
				ProjectRoot:        projectRoot,
				DefaultEnvironment: "dev-busy",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"team-busy/dev-busy": {
				Name:               "dev-busy",
				RepoPath:           projectRoot,
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Remote:             true,
			},
		},
	}
	stopped := make(chan string, 1)
	app := NewApp(erunUIDeps{
		store:           store,
		resolveCLIPath:  func() string { return "/tmp/erun" },
		findProjectRoot: func() (string, string, error) { return "project", projectRoot, nil },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		canConnectLocalPort: func(int) bool { return true },
		loadIdleStatus: func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error) {
			return eruncommon.EnvironmentIdleStatus{
				ManagedCloud: true,
				StopEligible: true,
				Policy: eruncommon.EnvironmentIdlePolicy{
					Timeout: 5 * time.Minute,
				},
				Markers: []eruncommon.EnvironmentIdleMarker{
					{Name: "working-hours", Idle: true},
					{Name: eruncommon.ActivityKindSSH, Idle: true},
					{Name: eruncommon.ActivityKindMCP, Idle: true},
					{Name: eruncommon.ActivityKindCLI, Idle: true},
					{Name: eruncommon.ActivityKindCodex, Idle: true},
				},
			}, nil
		},
		stopCloudContext: func(_ context.Context, name string) (eruncommon.CloudContextStatus, error) {
			stopped <- name
			return eruncommon.CloudContextStatus{}, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartDeploySession(uiSelection{Tenant: "team-busy", Environment: "dev-busy", Version: "1.0.0"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "team-busy", Environment: "dev-busy"}); err != nil {
		t.Fatalf("LoadIdleStatus failed: %v", err)
	}

	select {
	case got := <-stopped:
		t.Fatalf("did not expect cloud context stop while deploy is running, got %q", got)
	default:
	}
}

func TestIdleStatusToUIIncludesBlockerDetails(t *testing.T) {
	status := idleStatusToUI(eruncommon.EnvironmentIdleStatus{
		ManagedCloud:      true,
		StopEligible:      false,
		StopBlockedReason: "waiting for activity timeout",
		StopError:         "failed to stop instance: access denied",
		Policy: eruncommon.EnvironmentIdlePolicy{
			Timeout: 5 * time.Minute,
		},
		Markers: []eruncommon.EnvironmentIdleMarker{
			{Name: eruncommon.ActivityKindSSH, Idle: false, Reason: "recent activity", SecondsRemaining: 42},
			{Name: eruncommon.ActivityKindMCP, Idle: true, Reason: "last activity exceeded timeout"},
		},
	})

	assertIdleStatusBlockers(t, status)
}

func assertIdleStatusBlockers(t *testing.T, status uiIdleStatus) {
	t.Helper()

	if status.TimeoutSeconds != 300 || status.SecondsUntilStop != 42 || !status.ManagedCloud || status.StopEligible {
		t.Fatalf("unexpected idle status: %+v", status)
	}
	if status.StopBlockedReason != "waiting for activity timeout" {
		t.Fatalf("unexpected stop blocked reason: %q", status.StopBlockedReason)
	}
	if status.StopError != "failed to stop instance: access denied" {
		t.Fatalf("unexpected stop error: %q", status.StopError)
	}
	assertIdleStatusMarkers(t, status.Markers)
}

func assertIdleStatusMarkers(t *testing.T, markers []uiIdleMarker) {
	t.Helper()

	if len(markers) != 2 || markers[0].Name != eruncommon.ActivityKindSSH || markers[0].Reason != "recent activity" || markers[0].SecondsRemaining != 42 {
		t.Fatalf("unexpected markers: %+v", markers)
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
	config  *eruncommon.ERunConfig
	tenants map[string]eruncommon.TenantConfig
	envs    map[string]eruncommon.EnvConfig
}

func testCloudContextDeps(actions *[]string) eruncommon.CloudContextDependencies {
	return eruncommon.CloudContextDependencies{
		RunAWS: func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, _ string, args []string) (string, error) {
			*actions = append(*actions, "aws "+strings.Join(args, " "))
			if strings.Join(args, " ") == "ec2 describe-instances --instance-ids i-test --query Reservations[0].Instances[0].PublicIpAddress --output text" {
				return "203.0.113.10", nil
			}
			return "", nil
		},
		RunKubectl: func(_ eruncommon.Context, args []string) error {
			*actions = append(*actions, "kubectl "+strings.Join(args, " "))
			return nil
		},
	}
}

func (s stubUIStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	if s.config == nil {
		return eruncommon.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s stubUIStore) SaveERunConfig(config eruncommon.ERunConfig) error {
	if s.config != nil {
		*s.config = config
	}
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

func (s stubUIStore) SaveEnvConfig(tenant string, config eruncommon.EnvConfig) error {
	if s.envs == nil {
		s.envs = make(map[string]eruncommon.EnvConfig)
	}
	s.envs[tenant+"/"+config.Name] = config
	return nil
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
	waitErr error
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

func (s *stubTerminalSession) Wait() error {
	return s.waitErr
}

func (s *stubTerminalSession) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}
