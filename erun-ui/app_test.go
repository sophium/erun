package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
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
					{Name: "local", APIURL: "http://127.0.0.1:17033", RuntimeVersion: "1.0.19-snapshot-20260418141901", LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17000, API: 17033}, SSH: eruncommon.ListSSHResult{Enabled: true}},
					{Name: "remote", APIURL: "http://127.0.0.1:17133", RuntimeVersion: "1.0.18", Remote: true, LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17100, API: 17133}},
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
	if state.Tenants[0].Environments[0].APIURL != "http://127.0.0.1:17033" {
		t.Fatalf("unexpected API URL: %+v", state.Tenants[0].Environments[0])
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
		store:                stubUIStore{},
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
	if suggestions[0].Label != "frs latest stable" || suggestions[0].Image != eruncommon.DefaultContainerRegistry+"/frs-devops" || suggestions[3].Label != "ERun current" || suggestions[3].Image != eruncommon.DefaultContainerRegistry+"/"+eruncommon.DefaultRuntimeImageName {
		t.Fatalf("unexpected suggestion metadata: %+v", suggestions)
	}
}

func TestLoadVersionSuggestionsUsesEnvPersistedRuntimeRegistry(t *testing.T) {
	// #475: the "Version to deploy" picker must query the registry the env's
	// runtime image was actually published to (EnvConfig.RuntimeRegistry, the
	// provenance deploy records), not the hardcoded default. Otherwise an env on
	// a non-default registry resolves its suggestions from the wrong place and
	// can never offer its own deployed version back. The ERun fallback image
	// stays on the canonical default registry.
	const customRegistry = "harbor.example/team"
	var tenantImageNamespace string
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			envs: map[string]eruncommon.EnvConfig{
				"team/prod": {Name: "prod", RuntimeRegistry: customRegistry},
			},
		},
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			switch repository {
			case "team-devops":
				tenantImageNamespace = namespace
				return eruncommon.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.11", "1.0.12-snapshot-20260608120000"},
					LatestStable:   "1.0.11",
					LatestSnapshot: "1.0.12-snapshot-20260608120000",
				}, nil
			case eruncommon.DefaultRuntimeImageName:
				if namespace != eruncommon.DefaultContainerRegistry {
					t.Fatalf("ERun fallback image must use the default registry, got %s", namespace)
				}
				return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return eruncommon.RuntimeRegistryVersions{}, nil
		},
	})

	if _, err := app.LoadVersionSuggestions(uiSelection{Tenant: "team", Environment: "prod"}); err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	if tenantImageNamespace != customRegistry {
		t.Fatalf("tenant runtime image queried namespace %q, want %q", tenantImageNamespace, customRegistry)
	}
}

func TestLoadVersionSuggestionsQueriesEachListedRegistry(t *testing.T) {
	// #527: the version picker must query every registry in the env's marked
	// list, so an offered version can come from any listed registry and carries
	// its source. Here the env lists build+from on a public registry and
	// to+deploy on a mirror; both are queried for the tenant image.
	queried := map[string]bool{}
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			envs: map[string]eruncommon.EnvConfig{
				"team/prod": {
					Name: "prod",
					ContainerRegistries: eruncommon.ContainerRegistries{
						{Registry: "ghcr.io/acme", Roles: []eruncommon.RegistryRole{eruncommon.RegistryRoleBuild, eruncommon.RegistryRoleFrom}},
						{Registry: "registry.internal/acme", Roles: []eruncommon.RegistryRole{eruncommon.RegistryRoleTo, eruncommon.RegistryRoleDeploy}},
					},
				},
			},
		},
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if repository == "team-devops" {
				queried[namespace] = true
				return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository, Tags: []string{"1.0.11"}, LatestStable: "1.0.11"}, nil
			}
			return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
		},
	})

	if _, err := app.LoadVersionSuggestions(uiSelection{Tenant: "team", Environment: "prod"}); err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	for _, want := range []string{"ghcr.io/acme", "registry.internal/acme"} {
		if !queried[want] {
			t.Fatalf("expected the tenant image to be queried at %q; queried=%v", want, queried)
		}
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
		store:            stubUIStore{},
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
		store:            stubUIStore{},
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

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test "})
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
		store:            stubUIStore{},
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

	suggestions, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	got := versionValues(suggestions)
	want := []string{"1.0.48", "1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
	if suggestions[0].Image != eruncommon.DefaultContainerRegistry+"/test-devops" || suggestions[1].Image != eruncommon.DefaultContainerRegistry+"/"+eruncommon.DefaultRuntimeImageName {
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
	ensureCalls := 0
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		ensureMCP: func(_ context.Context, _ eruncommon.OpenResult) error {
			ensureCalls++
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
	if ensureCalls != 0 {
		t.Fatalf("LoadDiff must not implicitly run erun open; got ensureCalls=%d", ensureCalls)
	}
	if result.RawDiff == "" {
		t.Fatalf("unexpected diff result: %+v", result)
	}
}

func TestLoadDiffReturnsUnreachableWhenPortClosed(t *testing.T) {
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
			return false
		},
		ensureMCP: func(_ context.Context, _ eruncommon.OpenResult) error {
			ensureCalls++
			return nil
		},
		loadDiff: func(_ context.Context, _ string, _ uiDiffOptions) (eruncommon.DiffResult, error) {
			loadCalls++
			return eruncommon.DiffResult{}, nil
		},
	})

	_, err := app.LoadDiff(uiSelection{Tenant: "erun", Environment: "test"}, uiDiffOptions{})
	if err == nil {
		t.Fatalf("expected unreachable error when port is closed")
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if ensureCalls != 0 || loadCalls != 0 {
		t.Fatalf("LoadDiff must not run erun open or attempt the dial when the port is closed; got ensure=%d load=%d", ensureCalls, loadCalls)
	}
}

func TestLoadDiffWrapsDialFailureAsUnreachable(t *testing.T) {
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
	app := NewApp(erunUIDeps{
		store: store,
		canConnectLocalPort: func(int) bool {
			return true
		},
		ensureMCP: func(_ context.Context, _ eruncommon.OpenResult) error {
			ensureCalls++
			return nil
		},
		loadDiff: func(_ context.Context, _ string, _ uiDiffOptions) (eruncommon.DiffResult, error) {
			return eruncommon.DiffResult{}, errors.New("EOF")
		},
	})

	_, err := app.LoadDiff(uiSelection{Tenant: "erun", Environment: "test"}, uiDiffOptions{})
	if err == nil {
		t.Fatalf("expected dial failure to surface as unreachable")
	}
	if !errors.Is(err, errMCPUnreachable) {
		t.Fatalf("expected errMCPUnreachable, got %v", err)
	}
	if ensureCalls != 0 {
		t.Fatalf("LoadDiff must not implicitly run erun open after a dial failure; got ensureCalls=%d", ensureCalls)
	}
}

func TestReconnectMCPRunsOpenAndStreamsLines(t *testing.T) {
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
	calls := 0
	var lines []string
	app := NewApp(erunUIDeps{
		store: store,
		reconnectMCP: func(_ context.Context, result eruncommon.OpenResult, onLine func(string)) error {
			calls++
			if result.Tenant != "erun" || result.Environment != "test" {
				t.Fatalf("unexpected target: %+v", result)
			}
			onLine("step: deploying erun-devops")
			lines = append(lines, "step: deploying erun-devops")
			onLine("==> Deployed erun/test")
			lines = append(lines, "==> Deployed erun/test")
			return nil
		},
	})

	if err := app.ReconnectMCP(uiSelection{Tenant: " erun ", Environment: " test "}); err != nil {
		t.Fatalf("ReconnectMCP failed: %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected reconnect to invoke open exactly once, got %d", calls)
	}
	if len(lines) != 2 {
		t.Fatalf("expected two streamed lines, got %d", len(lines))
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

func TestBuildOpenIDEArgsAddsIDEFlag(t *testing.T) {
	got := buildOpenIDEArgs(uiSelection{Tenant: " erun ", Environment: " remote "}, "vscode")
	want := []string{"open", "erun", "remote", "--vscode"}
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
	got := buildDoctorArgs(uiSelection{Tenant: " erun ", Environment: " remote "})
	want := []string{"doctor", "erun", "remote"}
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
	want := []string{"init", "erun", "remote", "--type=remote-agent", "--set-default-tenant=false", "--confirm-environment=true"}
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
		SetDefaultTenant:  true,
	})
	want := []string{"init", "erun", "remote", "--type=remote-agent", "--version", "1.0.19", "--runtime-image", "erun-devops", "--runtime-cpu", "6", "--runtime-memory", "12Gi", "--kubernetes-context", "orbstack", "--container-registry", "erunpaas", "--set-default-tenant=true", "--confirm-environment=true", "--no-git"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsRespectsExplicitType(t *testing.T) {
	got := buildInitArgs(uiSelection{
		Tenant:        "erun",
		Environment:   "local",
		Type:          "local-agent",
		LocalRepoPath: "/Users/me/code/project",
	})
	want := []string{"init", "erun", "local", "--type=local-agent", "--project-root", "/Users/me/code/project", "--set-default-tenant=false", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildInitArgsRuntimeTypeOmitsProjectRoot(t *testing.T) {
	got := buildInitArgs(uiSelection{
		Tenant:        "erun",
		Environment:   "prod",
		Type:          "runtime",
		LocalRepoPath: "/should/be/ignored",
	})
	want := []string{"init", "erun", "prod", "--type=runtime", "--set-default-tenant=false", "--confirm-environment=true"}
	if len(got) != len(want) {
		t.Fatalf("unexpected args length: got %+v want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected arg[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

func TestBuildDeployArgsRunsDeploy(t *testing.T) {
	got := buildDeployArgs(uiSelection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "})
	want := []string{"deploy", "erun", "remote", "--version", "1.0.19"}
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

func TestStartInitSessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	if len(sessions) != 1 {
		t.Fatalf("expected 1 spawned session (Local), got %d", len(sessions))
	}
	written := sessions[0].WrittenString()
	wantSubstr := "/tmp/erun init erun remote --type=remote-agent --version 1.0.19 --kubernetes-context orbstack --container-registry erunpaas --set-default-tenant=true --confirm-environment=true --no-git\n"
	if !strings.Contains(written, wantSubstr) {
		t.Fatalf("expected Local pty to receive %q, got %q", wantSubstr, written)
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
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
}

func TestStartInitSessionReusesLocalAcrossInvocations(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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

	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.18"}, 80, 24); err != nil {
		t.Fatalf("first StartInitSession failed: %v", err)
	}
	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
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
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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

	result, err := app.StartSSHDInitSession(uiSelection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartSSHDInitSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	if !strings.Contains(written, "/tmp/erun sshd init erun remote\n") {
		t.Fatalf("expected sshd-init command in Local pty, got %q", written)
	}
}

func TestStartDoctorSessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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

	result, err := app.StartDoctorSession(uiSelection{Tenant: " erun ", Environment: " remote "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDoctorSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	if !strings.Contains(written, "/tmp/erun doctor erun remote\n") {
		t.Fatalf("expected doctor command in Local pty, got %q", written)
	}
}

func TestStartDeploySessionPipesCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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

	result, err := app.StartDeploySession(uiSelection{Tenant: " erun ", Environment: " remote ", Version: " 1.0.19 "}, 80, 24)
	if err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected local kind, got %q", result.Kind)
	}
	written := sessions[0].WrittenString()
	if !strings.Contains(written, "/tmp/erun deploy erun remote --version 1.0.19\n") {
		t.Fatalf("expected deploy command in Local pty, got %q", written)
	}
}

func TestRunErunCommandReusesLocalAndERunSpawnsSeparately(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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

	if _, err := app.StartInitSession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartInitSession failed: %v", err)
	}
	if _, err := app.StartDeploySession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if startCalls != 2 {
		t.Fatalf("expected Local + ERun spawn (2 calls), got %d", startCalls)
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
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:         "team-cloud",
		Provider:      eruncommon.CloudProviderAWS,
		OIDCIssuerURL: "https://issuer.team.example",
	}}}
	store := stubUIStore{
		config: &rootConfig,
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:                      "frs",
				ProjectRoot:               "/tmp/old",
				DefaultEnvironment:        "dev",
				APIURL:                    "https://api.old.example",
				CloudProviderAliases:      []string{"team-cloud"},
				PrimaryCloudProviderAlias: "team-cloud",
				Remote:                    true,
				Snapshot:                  &snapshot,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadTenantConfig(" frs ")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	if loaded.Name != "frs" || loaded.DefaultEnvironment != "dev" || loaded.APIURL != "https://api.old.example" || loaded.PrimaryCloudProviderAlias != "team-cloud" || len(loaded.CloudProviderAliases) != 1 || loaded.CloudProviderAliases[0] != "team-cloud" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if len(loaded.CloudProviders) != 1 || loaded.CloudProviders[0].OIDCIssuerURL != "https://issuer.team.example" {
		t.Fatalf("expected cloud provider statuses with issuer URL, got %+v", loaded.CloudProviders)
	}

	saved, err := app.SaveTenantConfig(uiTenantConfig{
		Name:                      "frs",
		DefaultEnvironment:        " prod ",
		APIURL:                    " https://api.new.example ",
		CloudProviderAliases:      []string{" team-cloud "},
		PrimaryCloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("SaveTenantConfig failed: %v", err)
	}
	if saved.DefaultEnvironment != "prod" || saved.APIURL != "https://api.new.example" || saved.PrimaryCloudProviderAlias != "team-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if store.tenants["frs"].ProjectRoot != "/tmp/old" || store.tenants["frs"].APIURL != "https://api.new.example" || store.tenants["frs"].PrimaryCloudProviderAlias != "team-cloud" || !store.tenants["frs"].Remote || store.tenants["frs"].Snapshot == nil || *store.tenants["frs"].Snapshot {
		t.Fatalf("expected tenant project root/remote/snapshot to be preserved, got %+v", store.tenants["frs"])
	}
}

func TestSetupCloudProviderOIDCStoresIssuer(t *testing.T) {
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: eruncommon.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	store := stubUIStore{config: &rootConfig}
	app := NewApp(erunUIDeps{
		store: store,
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(_ eruncommon.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != eruncommon.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return testUIJWT("https://sts.aws.example/.well-known/openid-configuration"), nil
			},
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})

	status, err := app.SetupCloudProviderOIDC("team-cloud")
	if err != nil {
		t.Fatalf("SetupCloudProviderOIDC failed: %v", err)
	}
	if status.OIDCIssuerURL != "https://sts.aws.example" || rootConfig.CloudProviders[0].OIDCIssuerURL != "https://sts.aws.example" {
		t.Fatalf("expected OIDC issuer to be stored, status=%+v config=%+v", status, rootConfig)
	}
}

func TestGetCloudProviderBearerTokenReturnsTokenAndStatus(t *testing.T) {
	jwt := testUIJWT("https://sts.aws.example/.well-known/openid-configuration")
	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: eruncommon.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(_ eruncommon.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != eruncommon.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer token input profile=%q audience=%q", profile, audience)
				}
				return jwt, nil
			},
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})

	token, err := app.GetCloudProviderBearerToken(" team-cloud ")
	if err != nil {
		t.Fatalf("GetCloudProviderBearerToken failed: %v", err)
	}
	if token.Token != jwt || token.Issuer != "https://sts.aws.example" {
		t.Fatalf("unexpected bearer token result: %+v", token)
	}
	if token.Provider.Alias != "team-cloud" || token.Provider.Status != eruncommon.CloudTokenStatusActive {
		t.Fatalf("expected active provider status, got %+v", token.Provider)
	}
}

func TestLoadTenantDashboardUsesPrimaryCloudBearer(t *testing.T) {
	jwt := testUIJWT("https://sts.aws.example")
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+jwt {
			t.Fatalf("unexpected authorization header: %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("X-ERun-Username") != "Rihards.Freimanis" {
			t.Fatalf("unexpected username hint: %q", req.Header.Get("X-ERun-Username"))
		}
		requests = append(requests, req.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch req.URL.Path {
		case "/v1/whoami":
			_, _ = w.Write([]byte(`{"tenantId":"tenant-1","userId":"user-1","username":"Rihards.Freimanis","roles":["ReadAll","WriteAll"],"issuer":"https://sts.aws.example","subject":"subject-1"}`))
		case "/v1/reviews":
			_, _ = w.Write([]byte(`[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`))
		case "/v1/reviews/merge-queue":
			_, _ = w.Write([]byte(`[{"reviewId":"review-1","tenantId":"tenant-1","name":"Review 1","targetBranch":"main","sourceBranch":"feature","status":"READY"}]`))
		case "/v1/reviews/review-1/builds":
			_, _ = w.Write([]byte(`[{"buildId":"build-1","tenantId":"tenant-1","reviewId":"review-1","successful":true,"commitId":"abc","version":"1.2.3"}]`))
		default:
			http.NotFound(w, req)
		}
	}))
	defer server.Close()

	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: eruncommon.CloudProviderAWS,
		Profile:  "team",
		Username: "Rihards.Freimanis",
	}}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(_ eruncommon.Context, profile, audience string) (string, error) {
				if profile != "team" || audience != eruncommon.CloudProviderBearerAudience {
					t.Fatalf("unexpected bearer input profile=%q audience=%q", profile, audience)
				}
				return jwt, nil
			},
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
	})

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{
		Tenant:             "frs",
		APIURL:             server.URL,
		CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if dashboard.User == nil || dashboard.User.Username != "Rihards.Freimanis" || len(dashboard.User.Roles) != 2 || len(dashboard.MergeQueue) != 1 || len(dashboard.Builds) != 1 || dashboard.Builds[0].ReviewName != "Review 1" {
		t.Fatalf("unexpected dashboard: %+v", dashboard)
	}
	if strings.Join(requests, ",") != "/v1/whoami,/v1/reviews,/v1/reviews/merge-queue,/v1/reviews/review-1/builds" {
		t.Fatalf("unexpected API requests: %+v", requests)
	}
}

func TestLoadTenantDashboardReturnsAPILogWhenAPIAuthFails(t *testing.T) {
	jwt := testUIJWT("https://sts.aws.example")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/whoami" {
			t.Fatalf("unexpected request path: %s", req.URL.Path)
		}
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()

	rootConfig := eruncommon.ERunConfig{CloudProviders: []eruncommon.CloudProviderConfig{{
		Alias:    "team-cloud",
		Provider: eruncommon.CloudProviderAWS,
		Profile:  "team",
	}}}
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: &rootConfig},
		cloudDeps: eruncommon.CloudDependencies{
			RunAWSBearerToken: func(eruncommon.Context, string, string) (string, error) {
				return jwt, nil
			},
			CheckAWSStatus: func(_ eruncommon.Context, provider eruncommon.CloudProviderConfig) eruncommon.CloudProviderStatus {
				return eruncommon.CloudProviderStatus{CloudProviderConfig: provider, Status: eruncommon.CloudTokenStatusActive}
			},
		},
		loadAPILog: func(_ context.Context, input uiTenantDashboardInput) (string, error) {
			if input.MCPURL != "http://127.0.0.1:17000/mcp" || input.KubernetesContext != "" {
				t.Fatalf("unexpected log input: %+v", input)
			}
			return "auth rejected token", nil
		},
	})

	dashboard, err := app.LoadTenantDashboard(uiTenantDashboardInput{
		Tenant:             "frs",
		APIURL:             server.URL,
		MCPURL:             "http://127.0.0.1:17000/mcp",
		CloudProviderAlias: "team-cloud",
	})
	if err != nil {
		t.Fatalf("LoadTenantDashboard failed: %v", err)
	}
	if !strings.Contains(dashboard.APIError, "/v1/whoami: 401") || dashboard.APILog != "auth rejected token" {
		t.Fatalf("unexpected dashboard: %+v", dashboard)
	}
}

func TestLoadAPILogPrefersKubernetesLogs(t *testing.T) {
	t.Setenv("PATH", fakeKubectl(t, func(args []string) (string, int) {
		got := strings.Join(args, "\n")
		want := "--context\ncluster-dev\n--namespace\nfrs-prod\nlogs\ndeployment/frs-devops\n-c\nerun-backend-api\n--tail\n400"
		if got != want {
			t.Fatalf("unexpected kubectl args:\n%s", got)
		}
		return "api log from pod\n", 0
	})+":"+os.Getenv("PATH"))

	log, err := loadAPILog(context.Background(), uiTenantDashboardInput{
		Tenant:            "frs",
		Environment:       "prod",
		MCPURL:            "http://127.0.0.1:17000/mcp",
		KubernetesContext: "cluster-dev",
	})
	if err != nil {
		t.Fatalf("loadAPILog failed: %v", err)
	}
	if log != "api log from pod" {
		t.Fatalf("unexpected log: %q", log)
	}
}

func fakeKubectl(t *testing.T, handler func([]string) (string, int)) string {
	t.Helper()
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "kubectl.args")
	outputPath := filepath.Join(dir, "kubectl.output")
	exitPath := filepath.Join(dir, "kubectl.exit")
	scriptPath := filepath.Join(dir, "kubectl")
	script := `#!/bin/sh
printf '%s\n' "$@" >"` + argsPath + `"
cat "` + outputPath + `"
exit "$(cat "` + exitPath + `")"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake kubectl failed: %v", err)
	}
	output, code := handler([]string{"--context", "cluster-dev", "--namespace", "frs-prod", "logs", "deployment/frs-devops", "-c", "erun-backend-api", "--tail", "400"})
	if err := os.WriteFile(outputPath, []byte(output), 0o644); err != nil {
		t.Fatalf("write fake kubectl output failed: %v", err)
	}
	if err := os.WriteFile(exitPath, []byte(fmt.Sprintf("%d", code)), 0o644); err != nil {
		t.Fatalf("write fake kubectl exit failed: %v", err)
	}
	t.Cleanup(func() {
		data, err := os.ReadFile(argsPath)
		if err != nil {
			t.Fatalf("read fake kubectl args failed: %v", err)
		}
		handler(strings.Split(strings.TrimSpace(string(data)), "\n"))
	})
	return dir
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

func testUIJWT(issuer string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"RS256","typ":"JWT"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"` + issuer + `"}`))
	return header + "." + payload + ".signature"
}

func TestLoadCloudContextStatusesRefreshesFromAWS(t *testing.T) {
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
				KubernetesContext:  "cluster-prod",
			},
		},
	}
	var awsCalls []string
	app := NewApp(erunUIDeps{
		store: stubUIStore{config: rootConfig},
		cloudContextDeps: eruncommon.CloudContextDependencies{
			RunAWS: func(_ eruncommon.Context, _ eruncommon.CloudProviderConfig, _ string, args []string) (string, error) {
				awsCalls = append(awsCalls, strings.Join(args, " "))
				return "i-test\tstopped\n", nil
			},
		},
	})

	statuses, err := app.LoadCloudContextStatuses()
	if err != nil {
		t.Fatalf("LoadCloudContextStatuses failed: %v", err)
	}
	if len(statuses) != 1 || statuses[0].Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected stale running status to be replaced with live stopped, got %+v", statuses)
	}
	if len(awsCalls) != 1 || !strings.Contains(awsCalls[0], "ec2 describe-instances") {
		t.Fatalf("expected one describe-instances call, got %+v", awsCalls)
	}
	// The settings refresh must populate the in-memory cache that the
	// idle widget and respawn gate now read from — otherwise the
	// titlebar would only converge to truth on the next 10s poll.
	if got := app.cloudContextStatus("team-context"); got != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected settings refresh to seed cloud-context cache with stopped, got %q", got)
	}
}

func TestApplyCloudContextStatusesToCachePreservesKnownStatusOnTransientUnknown(t *testing.T) {
	// AWS describe-instances failing for one region drives the
	// returned slice to Status=Unknown for the affected contexts.
	// Without this preservation, the next poll tick would blank a
	// "running" cache entry, hiding the idle widget for one cycle on
	// every transient SSO/network blip. Authoritative observations
	// must overwrite; Unknown must only land on previously-empty
	// slots.
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())

	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{
			CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx-a"},
			Status:             eruncommon.CloudContextStatusRunning,
		},
		{
			CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx-b"},
			Status:             eruncommon.CloudContextStatusStopped,
		},
	})
	if got := app.cloudContextStatus("ctx-a"); got != eruncommon.CloudContextStatusRunning {
		t.Fatalf("seed running, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-b"); got != eruncommon.CloudContextStatusStopped {
		t.Fatalf("seed stopped, got %q", got)
	}

	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{
		{
			CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx-a"},
			Status:             eruncommon.CloudContextStatusUnknown,
			Message:            "status refresh failed: token expired",
		},
		{
			CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx-b"},
			Status:             eruncommon.CloudContextStatusRunning,
		},
		{
			CloudContextConfig: eruncommon.CloudContextConfig{Name: "ctx-c"},
			Status:             eruncommon.CloudContextStatusUnknown,
		},
	})
	if got := app.cloudContextStatus("ctx-a"); got != eruncommon.CloudContextStatusRunning {
		t.Fatalf("transient unknown should not overwrite running, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-b"); got != eruncommon.CloudContextStatusRunning {
		t.Fatalf("authoritative transition stopped->running should overwrite, got %q", got)
	}
	if got := app.cloudContextStatus("ctx-c"); got != eruncommon.CloudContextStatusUnknown {
		t.Fatalf("first-observation unknown should populate empty slot, got %q", got)
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
			},
		},
	}
	store := stubUIStore{
		config: rootConfig,
		projectConfigs: map[string]eruncommon.ProjectConfig{
			projectRoot: {ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/project")},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:        "frs",
				ProjectRoot: projectRoot,
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				RepoPath:            projectRoot,
				KubernetesContext:   "cluster-old",
				ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/old"),
				CloudProviderAlias:  "team-cloud",
				RuntimeVersion:      "1.0.0",
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
	// Cloud-context Status is no longer persisted; seed the in-memory
	// cache that production code consults via linkedCloudContext.
	app.setCloudContextStatusInCache("team-context", eruncommon.CloudContextStatusStopped)

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

	if saved.RepoPath != projectRoot || saved.KubernetesContext != "cluster-old" || saved.ContainerRegistry != "registry.example/team" || saved.RuntimeVersion != "1.0.0" || saved.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if saved.RuntimePod.CPU != "6" || saved.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected saved runtime pod config: %+v", saved.RuntimePod)
	}
	assertLocalPorts(t, saved.LocalPorts)
}

func assertLocalPorts(t *testing.T, ports uiEnvironmentLocalPorts) {
	t.Helper()

	if ports.RangeStart != 17000 || ports.RangeEnd != 17099 || ports.MCP != 17000 || ports.API != 17033 || ports.SSH != 60022 {
		t.Fatalf("unexpected local ports: %+v", ports)
	}
}

func assertStoredEnvironmentConfig(t *testing.T, stored eruncommon.EnvConfig, projectRoot string) {
	t.Helper()

	storedRegistry, _ := stored.ContainerRegistries.BuildRegistry()
	if stored.RepoPath != projectRoot || stored.Remote || stored.RuntimeVersion != "1.0.0" || storedRegistry != "registry.example/team" || stored.CloudProviderAlias != "other-cloud" || stored.SSHD.Enabled || stored.SSHD.LocalPort != 60022 || stored.SSHD.PublicKeyPath != "/tmp/old.pub" || stored.Snapshot == nil || *stored.Snapshot {
		t.Fatalf("unexpected stored config: %+v", stored)
	}
	if stored.RuntimePod.CPU != "6" || stored.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected stored runtime pod config: %+v", stored.RuntimePod)
	}
}

func TestLoadEnvironmentConfigUsesProjectContainerRegistryForAllEnvironments(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]eruncommon.ProjectConfig{
			projectRoot: {
				ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/shared"),
				Environments: map[string]eruncommon.ProjectEnvironmentConfig{
					"prod": {ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/prod")},
				},
			},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:        "frs",
				ProjectRoot: projectRoot,
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-local",
			},
			"frs/prod": {
				Name:              "prod",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-prod",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	local, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig local failed: %v", err)
	}
	if local.ContainerRegistry != "registry.example/shared" {
		t.Fatalf("expected local env to use project-wide registry, got %+v", local)
	}

	prod, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig prod failed: %v", err)
	}
	if prod.ContainerRegistry != "registry.example/prod" {
		t.Fatalf("expected prod env to use environment registry override, got %+v", prod)
	}
}

func TestSaveEnvironmentConfigPreservesProjectContainerRegistryReadModel(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]eruncommon.ProjectConfig{
			projectRoot: {ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/shared")},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:        "frs",
				ProjectRoot: projectRoot,
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-local",
				RuntimePod: eruncommon.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "6",
			Memory: "12Gi",
		},
		Idle: uiIdleConfig{
			Timeout:      eruncommon.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: eruncommon.DefaultEnvironmentWorkingHours,
		},
		Snapshot: true,
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if saved.ContainerRegistry != "registry.example/shared" {
		t.Fatalf("expected saved UI config to keep effective project registry, got %+v", saved)
	}
	stored := store.envs["frs/local"]
	if len(stored.ContainerRegistries) != 0 {
		t.Fatalf("expected env save not to copy project registry into env config, got %+v", stored)
	}
}

func TestLoadEnvironmentConfigExposesClaudeDefaultsAndOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	useBedrock := false
	maxTokens := 8192
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", ProjectRoot: projectRoot},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-local",
				Claude: eruncommon.EnvironmentClaudeConfig{
					UseBedrock:      &useBedrock,
					Models:          []string{"opus", "sonnet"},
					MaxOutputTokens: &maxTokens,
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	got, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}

	if got.Claude.UseBedrock == nil || *got.Claude.UseBedrock {
		t.Fatalf("expected Claude.UseBedrock=false, got %+v", got.Claude)
	}
	if got.Claude.UseMantle != nil {
		t.Fatalf("expected Claude.UseMantle to remain unset, got %+v", got.Claude)
	}
	if got.Claude.MaxOutputTokens == nil || *got.Claude.MaxOutputTokens != 8192 {
		t.Fatalf("expected Claude.MaxOutputTokens=8192, got %+v", got.Claude)
	}
	if !equalStringSlices(got.Claude.Models, []string{"opus", "sonnet"}) {
		t.Fatalf("expected Claude.Models=[opus sonnet], got %+v", got.Claude.Models)
	}
	if got.ClaudeDefaults.MaxOutputTokens != eruncommon.DefaultClaudeMaxOutputTokens {
		t.Fatalf("expected default max output tokens, got %d", got.ClaudeDefaults.MaxOutputTokens)
	}
	if !equalStringSlices(got.ClaudeDefaults.Models, eruncommon.DefaultClaudeAvailableModels()) {
		t.Fatalf("expected default models, got %+v", got.ClaudeDefaults.Models)
	}
	if !equalStringSlices(got.ClaudeDefaults.KnownModels, eruncommon.KnownClaudeModels()) {
		t.Fatalf("expected known models list, got %+v", got.ClaudeDefaults.KnownModels)
	}
}

func TestSaveEnvironmentConfigRoundTripsClaudeOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", ProjectRoot: projectRoot},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-local",
				RuntimePod: eruncommon.RuntimePodResources{
					CPU:    "4",
					Memory: "8916Mi",
				},
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	useMantle := false
	maxTokens := 16384
	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		RuntimePod: uiRuntimePodConfig{
			CPU:    "4",
			Memory: "8916Mi",
		},
		Idle: uiIdleConfig{
			Timeout:      eruncommon.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: eruncommon.DefaultEnvironmentWorkingHours,
		},
		Claude: uiClaudeConfig{
			UseMantle:       &useMantle,
			Models:          []string{"opus"},
			MaxOutputTokens: &maxTokens,
		},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}

	if saved.Claude.UseMantle == nil || *saved.Claude.UseMantle {
		t.Fatalf("expected saved UseMantle=false, got %+v", saved.Claude)
	}
	if saved.Claude.UseBedrock != nil {
		t.Fatalf("expected saved UseBedrock unset, got %+v", saved.Claude)
	}
	stored := store.envs["frs/local"].Claude
	if stored.UseMantle == nil || *stored.UseMantle {
		t.Fatalf("expected stored UseMantle=false, got %+v", stored)
	}
	if stored.MaxOutputTokens == nil || *stored.MaxOutputTokens != 16384 {
		t.Fatalf("expected stored MaxOutputTokens=16384, got %+v", stored)
	}
	if !equalStringSlices(stored.Models, []string{"opus"}) {
		t.Fatalf("expected stored Models=[opus], got %+v", stored.Models)
	}
}

// TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags pins the desktop
// round-trip of the per-env Claude launch fields (issues #482/#477): the
// default model and the verbose+debug toggle survive a save both in the
// returned config and in the persisted store.
func TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", ProjectRoot: projectRoot},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-local",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	defaultModel := "fable"
	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-local",
		Idle: uiIdleConfig{
			Timeout:      eruncommon.DefaultEnvironmentIdleTimeout.String(),
			WorkingHours: eruncommon.DefaultEnvironmentWorkingHours,
		},
		Claude: uiClaudeConfig{
			Models:       []string{"opus", "fable"},
			DefaultModel: &defaultModel,
			VerboseDebug: true,
		},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}

	if saved.Claude.DefaultModel == nil || *saved.Claude.DefaultModel != "fable" {
		t.Fatalf("expected saved DefaultModel=fable, got %+v", saved.Claude)
	}
	if !saved.Claude.VerboseDebug {
		t.Fatalf("expected saved VerboseDebug=true, got %+v", saved.Claude)
	}
	stored := store.envs["frs/local"].Claude
	if stored.DefaultModel == nil || *stored.DefaultModel != "fable" {
		t.Fatalf("expected stored DefaultModel=fable, got %+v", stored)
	}
	if !stored.VerboseDebug {
		t.Fatalf("expected stored VerboseDebug=true, got %+v", stored)
	}
}

func TestSetEnvironmentAutoStartPersistsTriStateValue(t *testing.T) {
	// AutoStart is the desktop's per-env auto-start gate. The three modes
	// map to *bool: ask=nil (prompt on next open), always=true, never=false.
	// SetEnvironmentAutoStart must round-trip each mode through the store
	// without rewriting unrelated fields.
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", ProjectRoot: projectRoot},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				RepoPath:            projectRoot,
				KubernetesContext:   "cluster-prod",
				ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/keep"),
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "never")
	if err != nil {
		t.Fatalf("SetEnvironmentAutoStart(never) failed: %v", err)
	}
	if saved.AutoStart == nil || *saved.AutoStart != false {
		t.Fatalf("expected returned AutoStart=false, got %+v", saved.AutoStart)
	}
	if got := store.envs["frs/prod"].AutoStart; got == nil || *got != false {
		t.Fatalf("expected stored AutoStart=false, got %+v", got)
	}
	if keptRegistry, _ := store.envs["frs/prod"].ContainerRegistries.BuildRegistry(); keptRegistry != "registry.example/keep" {
		t.Fatalf("SetEnvironmentAutoStart must not rewrite unrelated fields, got %+v", store.envs["frs/prod"])
	}

	saved, err = app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "always")
	if err != nil {
		t.Fatalf("SetEnvironmentAutoStart(always) failed: %v", err)
	}
	if saved.AutoStart == nil || *saved.AutoStart != true {
		t.Fatalf("expected returned AutoStart=true, got %+v", saved.AutoStart)
	}

	saved, err = app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "ask")
	if err != nil {
		t.Fatalf("SetEnvironmentAutoStart(ask) failed: %v", err)
	}
	if saved.AutoStart != nil {
		t.Fatalf("expected returned AutoStart=nil after ask, got %+v", saved.AutoStart)
	}
	if got := store.envs["frs/prod"].AutoStart; got != nil {
		t.Fatalf("expected stored AutoStart=nil after ask, got %+v", got)
	}

	if _, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "bogus"); err == nil {
		t.Fatal("expected unknown auto-start mode to be rejected")
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
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

	if _, err := app.StartSession(uiSelection{Tenant: "frs", Environment: "prod"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	got := strings.Join(actions, "\n")
	// The ERun tab runs `erun open … --app-session open-0 --skip-ensure`: a
	// persistent, reattachable dtach session (#478) whose preflight runs once
	// per env via the shared ensure, not per tab (#463).
	if got != "terminal open frs prod --app-session open-0 --skip-ensure" {
		t.Fatalf("expected only terminal start action, got:\n%s", got)
	}
	// Cloud-context Status is no longer persisted, so we rely on the
	// action log above to prove the desktop ran no AWS start
	// commands — startup is `erun open`'s job.
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
	// Cloud-context Status is no longer persisted; the action log
	// above already proves `aws ec2 stop-instances` was issued, which
	// is the authoritative signal that the desktop stopped the linked
	// context after the namespace was deleted.
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

func TestStartSessionAllocatesIndependentSlots(t *testing.T) {
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

	first, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}
	second, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 1, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}
	reuse, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("third StartSession failed: %v", err)
	}

	if startCalls != 2 {
		t.Fatalf("start terminal called %d times, want 2", startCalls)
	}
	if first.SessionID == second.SessionID {
		t.Fatalf("expected distinct session ids for different slots, got %d for both", first.SessionID)
	}
	if reuse.SessionID != first.SessionID {
		t.Fatalf("expected slot 0 to reuse session %d, got %d", first.SessionID, reuse.SessionID)
	}
	if first.Slot != 0 || second.Slot != 1 {
		t.Fatalf("unexpected slot values: first=%d second=%d", first.Slot, second.Slot)
	}
}

func TestSendSessionInputRoutesPerSession(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}

	var stubs []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "", "", eruncommon.ErrNotInGitRepository },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			s := newStubTerminalSession()
			stubs = append(stubs, s)
			return s, nil
		},
	})
	defer app.shutdown(context.Background())

	first, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}
	second, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 1, 80, 24)
	if err != nil {
		t.Fatalf("second StartSession failed: %v", err)
	}

	if err := app.SendSessionInput(first.SessionID, "alpha"); err != nil {
		t.Fatalf("SendSessionInput first failed: %v", err)
	}
	if err := app.SendSessionInput(second.SessionID, "beta"); err != nil {
		t.Fatalf("SendSessionInput second failed: %v", err)
	}

	if len(stubs) != 2 {
		t.Fatalf("expected two terminal stubs, got %d", len(stubs))
	}
	if got := stubs[0].WrittenString(); got != "alpha" {
		t.Fatalf("slot 0 received %q, want %q", got, "alpha")
	}
	if got := stubs[1].WrittenString(); got != "beta" {
		t.Fatalf("slot 1 received %q, want %q", got, "beta")
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

	first, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartSession failed: %v", err)
	}

	second, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
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

	started, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if err := app.SendSessionInput(started.SessionID, "date\r"); err != nil {
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

func TestSendSessionInputClearsAwaitingPostRespawnInputFlag(t *testing.T) {
	// After a respawn, streamSession suppresses the 2s output-activity
	// ticker (reconnect noise must not count as user activity).
	// SendSessionInput is the signal that the user actually re-engaged
	// with the session, and it must clear the flag so subsequent
	// output once again refreshes the idle marker.
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local", RepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
		},
	}
	app := NewApp(erunUIDeps{
		store:          store,
		resolveCLIPath: func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		recordActivity: func(eruncommon.EnvironmentActivityParams) error { return nil },
	})
	defer app.shutdown(context.Background())

	started, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "local"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	app.mu.Lock()
	managed := app.sessionBySerialLocked(started.SessionID)
	if managed == nil {
		app.mu.Unlock()
		t.Fatalf("expected managed terminal for session %d", started.SessionID)
	}
	managed.awaitingPostRespawnInput = true
	app.mu.Unlock()

	if !app.isAwaitingPostRespawnInput(managed) {
		t.Fatal("expected flag to read true after manual set")
	}
	if err := app.SendSessionInput(started.SessionID, "ls\r"); err != nil {
		t.Fatalf("SendSessionInput failed: %v", err)
	}
	if app.isAwaitingPostRespawnInput(managed) {
		t.Fatal("expected SendSessionInput to clear awaitingPostRespawnInput flag")
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

// Note: the previous TestLoadIdleStatusStopsLinkedCloudContextWhenStopEligible
// test verified that LoadIdleStatus would itself fire ec2:StopInstances
// when the env became eligible. That behavior moved out of the desktop
// in PR #411 commit "Unify auto-stop grace period across desktop and
// in-pod monitor" — the shared `MaybeArmOrFireIdleStop` in erun-common
// is now the only arbiter and it is driven exclusively by the in-pod
// monitor via `erun activity stop-ready`. The desktop is a pure
// observer (renders the warning pill, exposes the Cancel button via
// MCP). The arm→wait→fire transitions are covered by integration
// scenarios under erun-integration/testdata/activity.

func TestStartCloudContextClearsPreviousIdleStop(t *testing.T) {
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:               "cloud-ctx",
				CloudProviderAlias: "team-cloud",
				KubernetesContext:  "cluster-cloud",
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

func TestIdleStatusToUIProjectsMarkerClients(t *testing.T) {
	// Locks the bridge between the per-IP marker data resolved by
	// erun-common and the JSON shape the desktop tooltip consumes. The
	// SSH proxy is the only kind that populates Clients today, but the
	// projection is generic so any future per-client surface gets the
	// same treatment without re-plumbing.
	status := idleStatusToUI(eruncommon.EnvironmentIdleStatus{
		Policy: eruncommon.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		Markers: []eruncommon.EnvironmentIdleMarker{
			{
				Name:             eruncommon.ActivityKindSSH,
				Idle:             false,
				Reason:           "recent activity",
				SecondsRemaining: 50,
				Clients: []eruncommon.EnvironmentIdleMarkerClient{
					{Address: "10.0.4.7", Bytes: 1500, SecondsAgo: 2},
					{Address: "127.0.0.1", Bytes: 548, SecondsAgo: 9},
				},
			},
			{Name: eruncommon.ActivityKindCLI, Idle: true, Reason: "no activity recorded"},
		},
	})
	if len(status.Markers) != 2 {
		t.Fatalf("expected 2 markers, got %+v", status.Markers)
	}
	ssh := status.Markers[0]
	if len(ssh.Clients) != 2 {
		t.Fatalf("expected SSH marker to carry 2 clients, got %+v", ssh.Clients)
	}
	if ssh.Clients[0].Address != "10.0.4.7" || ssh.Clients[0].Bytes != 1500 || ssh.Clients[0].SecondsAgo != 2 {
		t.Fatalf("unexpected first client: %+v", ssh.Clients[0])
	}
	if ssh.Clients[1].Address != "127.0.0.1" || ssh.Clients[1].Bytes != 548 || ssh.Clients[1].SecondsAgo != 9 {
		t.Fatalf("unexpected second client: %+v", ssh.Clients[1])
	}
	if status.Markers[1].Clients != nil {
		t.Fatalf("CLI marker should have no clients, got %+v", status.Markers[1].Clients)
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

	selection := uiSelection{Tenant: "erun", Environment: "local"}
	app.mu.Lock()
	app.nextSerial++
	serial := app.nextSerial
	managed := &managedTerminal{
		session:   newStubTerminalSession(),
		selection: selection,
		key:       openSessionKey(selection, 0),
		serial:    serial,
	}
	app.sessions[managed.key] = managed
	app.mu.Unlock()

	result, err := app.SavePastedImage(serial, pastedImagePayload{
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
	config         *eruncommon.ERunConfig
	projectConfigs map[string]eruncommon.ProjectConfig
	tenants        map[string]eruncommon.TenantConfig
	envs           map[string]eruncommon.EnvConfig
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
		// Pin "now" inside the default 08:00-20:00 working-hours window so
		// tests that exercise StartCloudContext are not sensitive to the
		// wall clock when the suite happens to run.
		Now: func() time.Time { return time.Date(2026, 5, 8, 12, 0, 0, 0, time.Local) },
	}
}

func (s stubUIStore) LoadERunConfig() (eruncommon.ERunConfig, string, error) {
	if s.config == nil {
		return eruncommon.ERunConfig{}, "", nil
	}
	return *s.config, "", nil
}

func (s stubUIStore) LoadProjectConfig(projectRoot string) (eruncommon.ProjectConfig, string, error) {
	config, ok := s.projectConfigs[projectRoot]
	if !ok {
		return eruncommon.ProjectConfig{}, "", eruncommon.ErrNotInitialized
	}
	return config, "", nil
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
	closeCh       chan struct{}
	waitErr       error
	mu            sync.Mutex
	written       []byte
	initialOutput []byte
}

// stubSessionReadyOutput is the line every newStubTerminalSession emits
// on its first Read so the desktop's session-ready trace handler
// observes the equivalent of `kubectl exec` attaching, signals the
// session ready, and releases the per-env action runner gate. Without
// this, tests calling StartSession twice in a row would wedge on the
// second call's enqueue waiting 10 min for the first action's gate.
const stubSessionReadyOutput = `Defaulted container "stub" out of: stub` + "\n"

func newStubTerminalSession() *stubTerminalSession {
	return &stubTerminalSession{
		closeCh:       make(chan struct{}),
		initialOutput: []byte(stubSessionReadyOutput),
	}
}

func (s *stubTerminalSession) Read(buf []byte) (int, error) {
	s.mu.Lock()
	if len(s.initialOutput) > 0 {
		n := copy(buf, s.initialOutput)
		s.initialOutput = s.initialOutput[n:]
		s.mu.Unlock()
		return n, nil
	}
	s.mu.Unlock()
	<-s.closeCh
	return 0, io.EOF
}

func (s *stubTerminalSession) Write(buffer []byte) (int, error) {
	s.mu.Lock()
	s.written = append(s.written, buffer...)
	s.mu.Unlock()
	return len(buffer), nil
}

func (s *stubTerminalSession) WrittenString() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.written)
}

func (s *stubTerminalSession) Resize(int, int) error {
	return nil
}

func (s *stubTerminalSession) Wait() error {
	return s.waitErr
}

func (s *stubTerminalSession) Pid() int {
	return 0
}

func (s *stubTerminalSession) Close() error {
	select {
	case <-s.closeCh:
	default:
		close(s.closeCh)
	}
	return nil
}

func TestStartLocalSessionStartsShellAtRepoPath(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var started startTerminalSessionParams
	var startCalls int
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		startTerminal: func(params startTerminalSessionParams) (terminalSession, error) {
			startCalls++
			started = params
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	result, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}
	if result.Kind != string(sessionKindLocal) {
		t.Fatalf("expected kind %q, got %q", sessionKindLocal, result.Kind)
	}
	if startCalls != 1 {
		t.Fatalf("startTerminal called %d times, want 1", startCalls)
	}
	if started.Dir != projectRoot {
		t.Fatalf("unexpected start dir: %q", started.Dir)
	}
	if strings.TrimSpace(started.Executable) == "" {
		t.Fatalf("expected non-empty shell executable, got %q", started.Executable)
	}
}

func TestStartAISessionRunsErunOpenAsPersistentAITab(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
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

	result, err := app.StartAISession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24)
	if err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}
	if result.Kind != string(sessionKindAI) {
		t.Fatalf("expected kind %q, got %q", sessionKindAI, result.Kind)
	}
	if started.Executable != "/tmp/erun" {
		t.Fatalf("expected erun executable, got %q", started.Executable)
	}
	// The AI tab runs `erun open --app-session ai --ai`: the persistent remote
	// session launches the AI tool itself (pod-side, once on create), so a reopen
	// reconnects to the running claude. The desktop no longer types the launch
	// in, so there is no initial input. The AI tool + effort are resolved pod-side
	// by `erun open --ai`; AISessionLaunchCommand is covered in erun-common. #478.
	// --skip-ensure: the preflight runs once per env via the shared ensure,
	// not per tab (#463).
	wantArgs := []string{"open", "erun", "remote", "--app-session", "ai", "--ai", "--skip-ensure"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
	if len(started.InitialInput) != 0 {
		t.Fatalf("AI tab must not pipe an initial input (claude launches pod-side), got %q", string(started.InitialInput))
	}
}

func TestStartSessionAutoReconnectsOnExit(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(sessions)
		mu.Unlock()
		if count >= 2 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected reconnect to spawn a second session, got %d", count)
	}
}

func TestStartSessionDoesNotReconnectIntoStoppedCloudContext(t *testing.T) {
	// The respawn loop used to re-launch `erun open` on every PTY exit,
	// even when the env's cloud context had just been auto-stopped. Each
	// respawn re-ran CloudContextPreflight, which immediately undid the
	// stop. Gate the respawn on cloud-context state so the auto-stop is
	// not fought by the desktop's own reconnect machinery; the user
	// recovers via the titlebar start button.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-cloud",
				Remote:            true,
			},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusStopped)

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	// Wait long enough that a respawn would have shown up if the gate
	// were broken; the existing reconnect test races for ~2s, so the
	// same window here would surface a regression.
	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		count := len(sessions)
		mu.Unlock()
		if count > 1 {
			t.Fatalf("expected no respawn against stopped cloud context, got %d sessions", count)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartAISessionRespawnsAfterStoppedCloudContextDeath(t *testing.T) {
	// When the linked cloud context auto-stops while a Claude/codex AI
	// session is attached, `tryReconnect` refuses to fight the stop and
	// `streamSession` cleans up the managed session (closed=true, removed
	// from a.sessions). The desktop's tab-respawn flow then calls
	// StartAISession again from the user's click on the dead AI tab; the
	// backend must respond with a brand new session instead of returning
	// the stale serial. This pins that contract so the click-driven
	// recovery in tabRespawnThunks does not regress into a no-op.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-cloud",
				Remote:            true,
			},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusStopped)

	selection := uiSelection{Tenant: "erun", Environment: "remote"}
	first, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("first StartAISession failed: %v", err)
	}

	mu.Lock()
	deadSession := sessions[0]
	mu.Unlock()
	_ = deadSession.Close()

	// Wait until streamSession finishes cleaning up the dead managed
	// terminal. The shouldRespawnForCloudContext gate is what makes this
	// observable: it sees the stopped context, refuses to respawn, and
	// drops the entry from a.sessions.
	key := aiSessionKey(selection, 0)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		_, present := app.sessions[key]
		app.mu.Unlock()
		if !present {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	app.mu.Lock()
	_, stillPresent := app.sessions[key]
	app.mu.Unlock()
	if stillPresent {
		t.Fatalf("expected streamSession to drop the dead AI session from a.sessions after the cloud-context gate refused respawn")
	}

	second, err := app.StartAISession(selection, 0, 80, 24)
	if err != nil {
		t.Fatalf("second StartAISession failed: %v", err)
	}
	if second.SessionID == first.SessionID {
		t.Fatalf("expected a fresh session id after restart, got the same serial %d", second.SessionID)
	}
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count < 2 {
		t.Fatalf("expected a second PTY spawn after click-driven respawn, got %d", count)
	}
}

func TestMaybeStopIdleClearsStaleIdleStopWhenContextIsRunningAgain(t *testing.T) {
	// idleStops latches on the first successful stop so the desktop
	// does not re-fire a stop while one is in flight. When the context
	// gets restarted externally (CLI preflight, manual titlebar Play,
	// `erun context start`), the desktop's flag turns stale and a
	// second auto-stop can never fire. maybeStopIdleCloudEnvironment
	// reconciles by clearing the flag whenever it observes a running
	// context. This test exercises the reconcile via the public
	// LoadIdleStatus path so the merge + recompute chain stays honest.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-cloud",
				Remote:            true,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return false },
		loadIdleStatus: func(context.Context, string) (eruncommon.EnvironmentIdleStatus, error) {
			return eruncommon.EnvironmentIdleStatus{}, fmt.Errorf("mcp unreachable")
		},
		stopCloudContext: func(context.Context, string) (eruncommon.CloudContextStatus, error) {
			t.Fatal("stop should not fire while context is running")
			return eruncommon.CloudContextStatus{}, nil
		},
	})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	key := selectionKey(uiSelection{Tenant: "erun", Environment: "remote"})
	app.mu.Lock()
	app.idleStops[key] = struct{}{}
	app.mu.Unlock()

	if _, err := app.LoadIdleStatus(uiSelection{Tenant: "erun", Environment: "remote"}); err != nil {
		t.Fatalf("LoadIdleStatus returned an error: %v", err)
	}

	app.mu.Lock()
	_, present := app.idleStops[key]
	app.mu.Unlock()
	if present {
		t.Fatal("expected stale idle-stop flag to be cleared while context is running")
	}
}

func TestIdleStatusToUIClearsStopErrorWhenContextIsRunning(t *testing.T) {
	// idle-stop.log lives on the pod's home PVC and survives pod and host
	// restarts. The runtime entrypoint truncates it on monitor start, but a
	// freshly-restarted env can still surface a poll-window racing the
	// truncate. The desktop guards against the stale error by suppressing
	// StopError whenever the linked cloud context is observably running —
	// a "stop failed" badge next to a healthy env is always wrong.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-cloud",
				Remote:            true,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusRunning)

	result, err := eruncommon.ResolveOpen(store, eruncommon.OpenParams{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	ui := app.idleStatusToUI(result, eruncommon.EnvironmentIdleStatus{
		ManagedCloud: true,
		Policy:       eruncommon.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		StopError:    "An error occurred (RequestExpired) when calling the StopInstances operation: Request has expired.",
	})

	if ui.CloudContextStatus != eruncommon.CloudContextStatusRunning {
		t.Fatalf("expected CloudContextStatus=%q, got %q", eruncommon.CloudContextStatusRunning, ui.CloudContextStatus)
	}
	if ui.StopError != "" {
		t.Fatalf("expected StopError to be cleared when context is running, got %q", ui.StopError)
	}
}

func TestIdleStatusToUIKeepsStopErrorWhenContextIsStopped(t *testing.T) {
	// Counterpart to the running-context test: when the linked cloud
	// context is not running, the stop error is still the desktop's only
	// surface for telling the user "the last auto-stop failed", so the
	// projection must pass it through unchanged.
	projectRoot := t.TempDir()
	store := stubUIStore{
		config: &eruncommon.ERunConfig{
			CloudContexts: []eruncommon.CloudContextConfig{{
				Name:              "managed-cloud",
				KubernetesContext: "cluster-cloud",
			}},
		},
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				RepoPath:          projectRoot,
				KubernetesContext: "cluster-cloud",
				Remote:            true,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})
	defer app.shutdown(context.Background())
	app.setCloudContextStatusInCache("managed-cloud", eruncommon.CloudContextStatusStopped)

	result, err := eruncommon.ResolveOpen(store, eruncommon.OpenParams{Tenant: "erun", Environment: "remote"})
	if err != nil {
		t.Fatalf("ResolveOpen failed: %v", err)
	}
	ui := app.idleStatusToUI(result, eruncommon.EnvironmentIdleStatus{
		ManagedCloud: true,
		Policy:       eruncommon.EnvironmentIdlePolicy{Timeout: 5 * time.Minute},
		StopError:    "An error occurred (RequestExpired) when calling the StopInstances operation: Request has expired.",
	})

	if ui.CloudContextStatus != eruncommon.CloudContextStatusStopped {
		t.Fatalf("expected CloudContextStatus=%q, got %q", eruncommon.CloudContextStatusStopped, ui.CloudContextStatus)
	}
	if ui.StopError == "" {
		t.Fatal("expected StopError to be preserved when context is stopped")
	}
}

func TestStartSessionLogsOpenCommandToLocal(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	if _, err := app.StartAISession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartAISession failed: %v", err)
	}

	app.mu.Lock()
	defer app.mu.Unlock()
	var local *managedTerminal
	for _, m := range app.sessions {
		if m != nil && m.kind == sessionKindLocal {
			local = m
			break
		}
	}
	if local == nil {
		t.Fatalf("Local session not found")
	}
	if _, ok := local.loggedCommands["erun"]; !ok {
		t.Fatalf("expected ERun command logged to Local, got %+v", local.loggedCommands)
	}
	if _, ok := local.loggedCommands["ai"]; !ok {
		t.Fatalf("expected AI command logged to Local, got %+v", local.loggedCommands)
	}
}

func TestStartSessionDoesNotLogWhenLocalAbsent(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
	})
	defer app.shutdown(context.Background())

	// No StartLocalSession — Local does not exist.
	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	// Should not crash. No assertion required beyond that.
}

func TestStartLocalSessionDoesNotAutoReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", ProjectRoot: projectRoot, DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", RepoPath: projectRoot, KubernetesContext: "ctx"},
		},
	}

	var sessions []*stubTerminalSession
	var mu sync.Mutex
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
	})
	defer app.shutdown(context.Background())

	if _, err := app.StartLocalSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartLocalSession failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close()

	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	count := len(sessions)
	mu.Unlock()
	if count != 1 {
		t.Fatalf("local session should not auto-reconnect; got %d spawn(s)", count)
	}
}
