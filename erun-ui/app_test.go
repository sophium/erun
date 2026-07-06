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
	"reflect"
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
					{Name: "remote", APIURL: "http://127.0.0.1:17133", RuntimeVersion: "1.0.18", Type: eruncommon.EnvironmentTypeRemoteAgent, LocalPorts: eruncommon.EnvironmentLocalPorts{MCP: 17100, API: 17133}},
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
	assertEffectiveSelectionEnvironments(t, state.Tenants[0].Environments)
}

func assertEffectiveSelectionEnvironments(t *testing.T, envs []uiEnvironment) {
	t.Helper()

	if envs[0].MCPURL != "http://127.0.0.1:17000/mcp" {
		t.Fatalf("unexpected MCP URL: %+v", envs[0])
	}
	if envs[0].APIURL != "http://127.0.0.1:17033" {
		t.Fatalf("unexpected API URL: %+v", envs[0])
	}
	if envs[0].RuntimeVersion != "1.0.19-snapshot-20260418141901" {
		t.Fatalf("unexpected runtime version: %+v", envs[0])
	}
	if !envs[0].SSHDEnabled || envs[1].SSHDEnabled {
		t.Fatalf("unexpected SSHD flags: %+v", envs)
	}
	if envs[0].Type == string(eruncommon.EnvironmentTypeRemoteAgent) || envs[1].Type != string(eruncommon.EnvironmentTypeRemoteAgent) {
		t.Fatalf("unexpected env types: %+v", envs)
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
					DefaultEnvironment: "prod",
				},
			},
			envs: map[string]eruncommon.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					LocalRepoPath:     projectRoot,
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

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: " frs "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	suggestions := result.Suggestions
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
	// The "Version to deploy" picker must query the registry the env's runtime
	// image was actually published to, not the hardcoded default — otherwise an
	// env on a non-default registry can never offer its own deployed version
	// back. The ERun fallback image stays on the canonical default registry.
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
	// The version picker must query every registry in the env's marked list, so
	// an offered version can come from any listed registry and carries its source.
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

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: " erun "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	suggestions := result.Suggestions
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

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	suggestions := result.Suggestions
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

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: " test "})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	suggestions := result.Suggestions
	got := versionValues(suggestions)
	want := []string{"1.0.48", "1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("unexpected suggestions: got %+v want %+v", suggestions, want)
	}
	if suggestions[0].Image != eruncommon.DefaultContainerRegistry+"/test-devops" || suggestions[1].Image != eruncommon.DefaultContainerRegistry+"/"+eruncommon.DefaultRuntimeImageName {
		t.Fatalf("unexpected suggestion metadata: %+v", suggestions)
	}
}

func TestLoadVersionSuggestionsSurfacesAuthNoticeForPrivateImage(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if repository == "frs-devops" {
				return eruncommon.RuntimeRegistryVersions{}, fmt.Errorf("ghcr tags request failed: 401 Unauthorized: %w", eruncommon.ErrRegistryAuthRequired)
			}
			return eruncommon.RuntimeRegistryVersions{
				Image:        namespace + "/" + repository,
				Tags:         []string{"1.0.50", "1.0.49"},
				LatestStable: "1.0.50",
			}, nil
		},
	})

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	// The private tenant image contributes an auth notice; the canonical ERun
	// image still lists its versions.
	if got := versionValues(result.Suggestions); strings.Join(got, "\n") != strings.Join([]string{"1.0.50", "1.0.49"}, "\n") {
		t.Fatalf("unexpected suggestions: %+v", result.Suggestions)
	}
	if len(result.Notices) != 1 {
		t.Fatalf("expected one notice, got %+v", result.Notices)
	}
	if notice := result.Notices[0]; notice.Kind != "auth" || notice.Image != eruncommon.DefaultContainerRegistry+"/frs-devops" {
		t.Fatalf("unexpected notice: %+v", notice)
	}
}

func TestLoadVersionSuggestionsMarksUnreachableRegistry(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() eruncommon.BuildInfo { return eruncommon.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if repository == "frs-devops" {
				return eruncommon.RuntimeRegistryVersions{}, fmt.Errorf("dial tcp: connection refused")
			}
			return eruncommon.RuntimeRegistryVersions{Image: namespace + "/" + repository, Tags: []string{"1.0.50"}, LatestStable: "1.0.50"}, nil
		},
	})

	result, err := app.LoadVersionSuggestions(uiSelection{Tenant: "frs"})
	if err != nil {
		t.Fatalf("LoadVersionSuggestions failed: %v", err)
	}
	if len(result.Notices) != 1 || result.Notices[0].Kind != "unreachable" {
		t.Fatalf("expected one unreachable notice, got %+v", result.Notices)
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
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
		loadDiff: func(_ context.Context, endpoint, _ string, options uiDiffOptions) (eruncommon.DiffResult, error) {
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
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
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
		loadDiff: func(_ context.Context, _, _ string, _ uiDiffOptions) (eruncommon.DiffResult, error) {
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
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
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
		loadDiff: func(_ context.Context, _, _ string, _ uiDiffOptions) (eruncommon.DiffResult, error) {
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
				DefaultEnvironment: "test",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/test": {
				Name:              "test",
				LocalRepoPath:     projectRoot,
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
	assertArgsEqual(t, got, want)
}

// TestBuildDeployArgsThreadsRuntimeImageOverride covers the runtime-image
// override that lets an operator bootstrap an env on the ERun base image before
// the tenant's own image is built.
func TestBuildDeployArgsThreadsRuntimeImageOverride(t *testing.T) {
	got := buildDeployArgs(uiSelection{
		Tenant:       "frs",
		Environment:  "local",
		Version:      "1.0.102",
		RuntimeImage: "ghcr.io/sophium/erun-devops",
	})
	want := []string{"deploy", "frs", "local", "--version", "1.0.102", "--runtime-image", "ghcr.io/sophium/erun-devops"}
	assertArgsEqual(t, got, want)
}

// TestBuildDeployArgsOmitsOwnTenantImage covers the counterpart: picking the
// env's own image is not an override.
func TestBuildDeployArgsOmitsOwnTenantImage(t *testing.T) {
	got := buildDeployArgs(uiSelection{
		Tenant:       "frs",
		Environment:  "local",
		Version:      "1.0.102",
		RuntimeImage: "ghcr.io/sophium/frs-devops",
	})
	want := []string{"deploy", "frs", "local", "--version", "1.0.102"}
	assertArgsEqual(t, got, want)
}

func assertArgsEqual(t *testing.T, got, want []string) {
	t.Helper()
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

func TestResolveCLIExecutableHonorsAppCLISeam(t *testing.T) {
	// The Playwright harness sets ERUN_APP_CLI to its inert `erun` stub so the
	// desktop runs the stub regardless of any real erun-cli/bin/erun build
	// artifact sitting next to the app binary. The seam wins over the
	// build-relative + PATH resolution.
	t.Setenv("ERUN_APP_CLI", "/seam/stub/erun")
	if got := resolveCLIExecutable(); got != "/seam/stub/erun" {
		t.Fatalf("resolveCLIExecutable() = %q, want the ERUN_APP_CLI override", got)
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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

// TestStartDeploySessionInjectsMCPAuthPublicKey covers the auth-injecting deploy
// path: with a signing identity, the in-shell deploy requires the same
// desktop-signed bearer the desktop sends to its MCP edge.
func TestStartDeploySessionInjectsMCPAuthPublicKey(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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

	if _, err := app.StartDeploySession(uiSelection{Tenant: "erun", Environment: "remote", Version: "1.0.19"}, 80, 24); err != nil {
		t.Fatalf("StartDeploySession failed: %v", err)
	}
	wantPath := filepath.Join(identityDir, desktopIdentityPubFile)
	written := sessions[0].WrittenString()
	// The temp-dir path is shell-quoted only when it needs it (e.g. spaces), so
	// assert against the same quoting helper rather than a fixed literal.
	wantFlag := "--mcp-auth-public-key " + shellQuoteIfNeeded(wantPath)
	if !strings.Contains(written, "deploy erun remote --version 1.0.19 "+wantFlag+"\n") {
		t.Fatalf("expected deploy command to carry %q, got %q", wantFlag, written)
	}
	if _, err := os.Stat(wantPath); err != nil {
		t.Fatalf("expected desktop public key written to %q: %v", wantPath, err)
	}
}

func TestRunErunCommandReusesLocalAndERunSpawnsSeparately(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
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
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
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
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "remote",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {
				Name:               "frs",
				DefaultEnvironment: "prod",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     "/home/erun/git/frs",
				KubernetesContext: "cluster-prod",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
				DefaultEnvironment:        "dev",
				APIURL:                    "https://api.old.example",
				CloudProviderAliases:      []string{"team-cloud"},
				PrimaryCloudProviderAlias: "team-cloud",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	loaded, err := app.LoadTenantConfig(" frs ")
	if err != nil {
		t.Fatalf("LoadTenantConfig failed: %v", err)
	}
	assertLoadedTenantConfig(t, loaded)

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
	if store.tenants["frs"].APIURL != "https://api.new.example" || store.tenants["frs"].PrimaryCloudProviderAlias != "team-cloud" {
		t.Fatalf("expected tenant project root to be preserved, got %+v", store.tenants["frs"])
	}
}

func assertLoadedTenantConfig(t *testing.T, loaded uiTenantConfig) {
	t.Helper()

	if loaded.Name != "frs" || loaded.DefaultEnvironment != "dev" || loaded.APIURL != "https://api.old.example" || loaded.PrimaryCloudProviderAlias != "team-cloud" || len(loaded.CloudProviderAliases) != 1 || loaded.CloudProviderAliases[0] != "team-cloud" {
		t.Fatalf("unexpected loaded config: %+v", loaded)
	}
	if len(loaded.CloudProviders) != 1 || loaded.CloudProviders[0].OIDCIssuerURL != "https://issuer.team.example" {
		t.Fatalf("expected cloud provider statuses with issuer URL, got %+v", loaded.CloudProviders)
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
	server := httptest.NewServer(tenantDashboardHandler(t, jwt, &requests))
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
	assertPrimaryCloudDashboard(t, dashboard, requests)
}

func tenantDashboardHandler(t *testing.T, jwt string, requests *[]string) http.HandlerFunc {
	t.Helper()

	return func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer "+jwt {
			t.Fatalf("unexpected authorization header: %q", req.Header.Get("Authorization"))
		}
		if req.Header.Get("X-ERun-Username") != "Rihards.Freimanis" {
			t.Fatalf("unexpected username hint: %q", req.Header.Get("X-ERun-Username"))
		}
		*requests = append(*requests, req.URL.Path)
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
	}
}

func assertPrimaryCloudDashboard(t *testing.T, dashboard uiTenantDashboard, requests []string) {
	t.Helper()

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
				Name: "frs",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				LocalRepoPath:       projectRoot,
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
				Type: eruncommon.EnvironmentTypeLocalAgent,
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
		Name:              "prod",
		RepoPath:          " /tmp/repo ",
		KubernetesContext: " cluster-new ",
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: " registry.example/team ", Roles: []string{"build", "deploy"}},
		},
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
		Type: eruncommon.EnvironmentTypeRuntime,
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	assertSavedEnvironmentConfig(t, saved, projectRoot)
	stored := store.envs["frs/prod"]
	assertStoredEnvironmentConfig(t, stored, projectRoot)
}

// TestSaveAndLoadDeployComponentsRoundTrip exercises the non-empty deploy-
// selection path the Playwright checklist cannot reach: the headless baseline
// vendors no local component charts to select.
func TestSaveAndLoadDeployComponentsRoundTrip(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &eruncommon.ERunConfig{},
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:           "prod",
				LocalRepoPath:  projectRoot,
				RuntimeVersion: "1.0.0",
				Type:           eruncommon.EnvironmentTypeLocalAgent,
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name:             "prod",
		RepoPath:         projectRoot,
		DeployComponents: []string{"frs-backend-postgres", " ", "frs-backend-api"},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	want := []string{"frs-backend-postgres", "frs-backend-api"}
	if !reflect.DeepEqual(saved.DeployComponents, want) {
		t.Fatalf("saved DeployComponents = %v, want %v (trimmed)", saved.DeployComponents, want)
	}
	if got := store.envs["frs/prod"].Deploy.Components; !reflect.DeepEqual(got, want) {
		t.Fatalf("persisted deploy.components = %v, want %v", got, want)
	}
	loaded, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig failed: %v", err)
	}
	if !reflect.DeepEqual(loaded.DeployComponents, want) {
		t.Fatalf("loaded DeployComponents = %v, want %v", loaded.DeployComponents, want)
	}
}

// TestLoadDeployComponentsRuntimeOnlyWhenNoLocalCharts covers the read model for
// an inert env: with no repo-local charts it offers only the runtime item,
// pre-selected as the bootstrap/heal default.
// TestLoadDeployComponentsLocalAgentShowsPublishedVersionView asserts the
// checklist is a published-version view for a LOCAL-agent env too: it lists the
// canonical component charts published at the deploy version (by reference) plus
// the runtime — identical to what a runtime env shows — never the env's local
// working-tree chart directories. The version, not the env's local source,
// decides which charts exist. The registry probe is injected so this is offline.
func TestLoadDeployComponentsLocalAgentShowsPublishedVersionView(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &eruncommon.ERunConfig{},
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              eruncommon.EnvironmentTypeLocalAgent,
			},
		},
	}
	// All five component charts are published at 1.0.0; none at 1.0.106. The frs
	// tenant publishes its own charts (frs-backend-*), not the canonical erun set.
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.0"},
		"charts/frs-backend-db":       {"1.0.0"},
		"charts/frs-backend-api":      {"1.0.0"},
		"charts/frs-powerdns":         {"1.0.0"},
		"charts/frs-docs":             {"1.0.0"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return eruncommon.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []eruncommon.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	// At 1.0.0 the local-agent env offers the published component charts + runtime —
	// the same list a runtime env would show, not local frs-* chart dirs.
	at100, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "local", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.0) failed: %v", err)
	}
	wantAt100 := []string{
		"frs-devops", "frs-backend-postgres", "frs-backend-db",
		"frs-backend-api", "frs-powerdns", "frs-docs",
	}
	if got := names(at100); !reflect.DeepEqual(got, wantAt100) {
		t.Fatalf("components at 1.0.0 = %v, want %v (published-version view, runtime first)", got, wantAt100)
	}
	runtime := at100[0]
	if !runtime.Runtime || !runtime.Selected || runtime.Source != "published-chart" {
		t.Fatalf("runtime item = %+v, want {frs-devops runtime selected published-chart}", runtime)
	}

	// With no version-to-deploy the list uses the env's current version (1.0.106),
	// which publishes no component charts here — runtime only.
	atCurrent, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current 1.0.106 = %v, want %v (runtime only)", got, want)
	}
}

// TestLoadDeployComponentsVersionAwareFiltersUnavailableCharts covers the
// version-aware checklist for a sourceless (runtime) env: only component charts
// the registry publishes at the deploy version are offered, and the runtime item
// is always kept. The registry probe cannot be driven from the headless
// Playwright suite (it stubs the network), so the filtering is asserted here.
func TestLoadDeployComponentsVersionAwareFiltersUnavailableCharts(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &eruncommon.ERunConfig{},
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              eruncommon.EnvironmentTypeRuntime,
			},
		},
	}
	// charts/<component> tags per registry repo: postgres + api are published at
	// 1.0.112 only; db, powerdns, docs are published nowhere in this fixture. The
	// frs tenant publishes its own charts (frs-backend-*).
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.112"},
		"charts/frs-backend-api":      {"1.0.112"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (eruncommon.RuntimeRegistryVersions, error) {
			if namespace != eruncommon.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return eruncommon.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []eruncommon.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	// At 1.0.112 only postgres + api charts exist, so only those (in rank order)
	// plus the always-kept runtime are offered.
	at112, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "prod", Version: "1.0.112"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.112) failed: %v", err)
	}
	if got, want := names(at112), []string{"frs-devops", "frs-backend-postgres", "frs-backend-api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at 1.0.112 = %v, want %v (runtime first; unpublished db/powerdns/docs filtered out)", got, want)
	}

	// With no version-to-deploy the list uses the env's current runtime version
	// (1.0.106), which has no published component charts here — runtime only.
	atCurrent, err := app.LoadDeployComponents(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current version 1.0.106 = %v, want %v (runtime only)", got, want)
	}
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

	if saved.RepoPath != projectRoot || saved.KubernetesContext != "cluster-old" || uiRegistryWithRole(saved.ContainerRegistries, "build") != "registry.example/team" || saved.RuntimeVersion != "1.0.0" || saved.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected saved config: %+v", saved)
	}
	if saved.RuntimePod.CPU != "6" || saved.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected saved runtime pod config: %+v", saved.RuntimePod)
	}
	assertLocalPorts(t, saved.LocalPorts)
}

func uiRegistryWithRole(entries []uiContainerRegistryEntry, role string) string {
	for _, entry := range entries {
		for _, candidate := range entry.Roles {
			if candidate == role {
				return entry.Registry
			}
		}
	}
	return ""
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
	if stored.EffectiveLocalRepoPath() != projectRoot || stored.Type != eruncommon.EnvironmentTypeRuntime || stored.RuntimeVersion != "1.0.0" || storedRegistry != "registry.example/team" || stored.CloudProviderAlias != "other-cloud" {
		t.Fatalf("unexpected stored config: %+v", stored)
	}
	assertStoredEnvironmentSSHD(t, stored)
	if stored.RuntimePod.CPU != "6" || stored.RuntimePod.Memory != "12Gi" {
		t.Fatalf("unexpected stored runtime pod config: %+v", stored.RuntimePod)
	}
}

func assertStoredEnvironmentSSHD(t *testing.T, stored eruncommon.EnvConfig) {
	t.Helper()

	if stored.SSHD.Enabled || stored.SSHD.LocalPort != 60022 || stored.SSHD.PublicKeyPath != "/tmp/old.pub" {
		t.Fatalf("unexpected stored config: %+v", stored)
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
				Name: "frs",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-local",
			},
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-prod",
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	local, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig local failed: %v", err)
	}
	if uiRegistryWithRole(local.ContainerRegistries, "build") != "registry.example/shared" {
		t.Fatalf("expected local env to use project-wide registry, got %+v", local)
	}

	prod, err := app.LoadEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadEnvironmentConfig prod failed: %v", err)
	}
	if uiRegistryWithRole(prod.ContainerRegistries, "build") != "registry.example/prod" {
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
				Name: "frs",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig failed: %v", err)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "build") != "registry.example/shared" {
		t.Fatalf("expected saved UI config to keep effective project registry, got %+v", saved)
	}
	stored := store.envs["frs/local"]
	if len(stored.ContainerRegistries) != 0 {
		t.Fatalf("expected env save not to copy project registry into env config, got %+v", stored)
	}
}

// TestSaveEnvironmentConfigWritesLocalAgentRegistryListToProjectConfig pins that
// editing a local-agent env's registry list writes to the project config —
// where the build/deploy resolvers read it — not to the env config.
func TestSaveEnvironmentConfigWritesLocalAgentRegistryListToProjectConfig(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		projectConfigs: map[string]eruncommon.ProjectConfig{
			projectRoot: {ContainerRegistries: eruncommon.SingleContainerRegistries("ghcr.io/acme")},
		},
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "c", Type: eruncommon.EnvironmentTypeLocalAgent},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:              "local",
		RepoPath:          projectRoot,
		KubernetesContext: "c",
		Type:              eruncommon.EnvironmentTypeLocalAgent,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/acme", Roles: []string{"build", "from"}},
			{Registry: "registry.internal/acme", Roles: []string{"to", "deploy"}},
		},
		Idle: uiIdleConfig{Timeout: eruncommon.DefaultEnvironmentIdleTimeout.String(), WorkingHours: eruncommon.DefaultEnvironmentWorkingHours},
	})
	if err != nil {
		t.Fatalf("SaveEnvironmentConfig: %v", err)
	}
	if len(store.envs["frs/local"].ContainerRegistries) != 0 {
		t.Fatalf("a local-agent env must not carry the list on env config, got %+v", store.envs["frs/local"].ContainerRegistries)
	}
	list := store.projectConfigs[projectRoot].ContainerRegistriesForEnvironment("local")
	if from, ok := list.FromRegistry(); !ok || from != "ghcr.io/acme" {
		t.Fatalf("expected from=ghcr.io/acme in project config, got %+v", list)
	}
	if deploy, ok := list.DeployRegistry(); !ok || deploy != "registry.internal/acme" {
		t.Fatalf("expected deploy=registry.internal/acme in project config, got %+v", list)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "deploy") != "registry.internal/acme" {
		t.Fatalf("saved UI must reflect the edited list, got %+v", saved.ContainerRegistries)
	}
}

// TestSaveEnvironmentConfigRejectsInvalidRegistryList confirms marker invariants
// are enforced before persistence: a list with no deploy registry is rejected.
func TestSaveEnvironmentConfigRejectsInvalidRegistryList(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {Name: "local", LocalRepoPath: projectRoot, Type: eruncommon.EnvironmentTypeLocalAgent},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	_, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "local"}, uiEnvironmentConfig{
		Name:     "local",
		RepoPath: projectRoot,
		Type:     eruncommon.EnvironmentTypeLocalAgent,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/acme", Roles: []string{"build"}},
		},
		Idle: uiIdleConfig{Timeout: eruncommon.DefaultEnvironmentIdleTimeout.String(), WorkingHours: eruncommon.DefaultEnvironmentWorkingHours},
	})
	if err == nil {
		t.Fatal("expected a validation error for a registry list with no deploy registry")
	}
}

// TestSaveEnvironmentConfigAcceptsDeployOnlyRegistry pins that a registry
// marked deploy-only is valid: the image it serves may be published there
// externally (a runtime env pulling a released image), so erun must not force
// a build or to role on it.
func TestSaveEnvironmentConfigAcceptsDeployOnlyRegistry(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {Name: "prod", LocalRepoPath: projectRoot, Type: eruncommon.EnvironmentTypeRuntime},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	saved, err := app.SaveEnvironmentConfig(uiSelection{Tenant: "frs", Environment: "prod"}, uiEnvironmentConfig{
		Name: "prod",
		Type: eruncommon.EnvironmentTypeRuntime,
		ContainerRegistries: []uiContainerRegistryEntry{
			{Registry: "ghcr.io/sophium", Roles: []string{"deploy"}},
		},
		Idle: uiIdleConfig{Timeout: eruncommon.DefaultEnvironmentIdleTimeout.String(), WorkingHours: eruncommon.DefaultEnvironmentWorkingHours},
	})
	if err != nil {
		t.Fatalf("deploy-only registry must be accepted, got %v", err)
	}
	if uiRegistryWithRole(saved.ContainerRegistries, "deploy") != "ghcr.io/sophium" {
		t.Fatalf("expected the deploy-only registry to persist, got %+v", saved.ContainerRegistries)
	}
}

func TestLoadEnvironmentConfigExposesClaudeDefaultsAndOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	useBedrock := false
	maxTokens := 8192
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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

	assertClaudeOverrides(t, got.Claude)
	assertClaudeDefaults(t, got.ClaudeDefaults)
}

func assertClaudeOverrides(t *testing.T, claude uiClaudeConfig) {
	t.Helper()

	if claude.UseBedrock == nil || *claude.UseBedrock {
		t.Fatalf("expected Claude.UseBedrock=false, got %+v", claude)
	}
	if claude.UseMantle != nil {
		t.Fatalf("expected Claude.UseMantle to remain unset, got %+v", claude)
	}
	if claude.MaxOutputTokens == nil || *claude.MaxOutputTokens != 8192 {
		t.Fatalf("expected Claude.MaxOutputTokens=8192, got %+v", claude)
	}
	if !equalStringSlices(claude.Models, []string{"opus", "sonnet"}) {
		t.Fatalf("expected Claude.Models=[opus sonnet], got %+v", claude.Models)
	}
}

func assertClaudeDefaults(t *testing.T, defaults uiClaudeDefaults) {
	t.Helper()

	if defaults.MaxOutputTokens != eruncommon.DefaultClaudeMaxOutputTokens {
		t.Fatalf("expected default max output tokens, got %d", defaults.MaxOutputTokens)
	}
	if !equalStringSlices(defaults.Models, eruncommon.DefaultClaudeAvailableModels()) {
		t.Fatalf("expected default models, got %+v", defaults.Models)
	}
	if !equalStringSlices(defaults.KnownModels, eruncommon.KnownClaudeModels()) {
		t.Fatalf("expected known models list, got %+v", defaults.KnownModels)
	}
}

func TestSaveEnvironmentConfigRoundTripsClaudeOverrides(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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

// TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags pins the desktop round-
// trip of the per-env Claude launch fields (default model, verbose+debug).
func TestSaveEnvironmentConfigRoundTripsClaudeLaunchFlags(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
			"frs": {Name: "frs"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:                "prod",
				LocalRepoPath:       projectRoot,
				KubernetesContext:   "cluster-prod",
				ContainerRegistries: eruncommon.SingleContainerRegistries("registry.example/keep"),
			},
		},
	}
	app := NewApp(erunUIDeps{store: store})

	never := false
	saved := setAutoStartMode(t, app, "never")
	assertReturnedAutoStart(t, saved.AutoStart, &never, "false")
	assertStoredAutoStart(t, store.envs["frs/prod"].AutoStart, &never, "false")
	if keptRegistry, _ := store.envs["frs/prod"].ContainerRegistries.BuildRegistry(); keptRegistry != "registry.example/keep" {
		t.Fatalf("SetEnvironmentAutoStart must not rewrite unrelated fields, got %+v", store.envs["frs/prod"])
	}

	always := true
	saved = setAutoStartMode(t, app, "always")
	assertReturnedAutoStart(t, saved.AutoStart, &always, "true")

	saved = setAutoStartMode(t, app, "ask")
	assertReturnedAutoStart(t, saved.AutoStart, nil, "nil after ask")
	assertStoredAutoStart(t, store.envs["frs/prod"].AutoStart, nil, "nil after ask")

	if _, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, "bogus"); err == nil {
		t.Fatal("expected unknown auto-start mode to be rejected")
	}
}

func setAutoStartMode(t *testing.T, app *App, mode string) uiEnvironmentConfig {
	t.Helper()

	saved, err := app.SetEnvironmentAutoStart(uiSelection{Tenant: "frs", Environment: "prod"}, mode)
	if err != nil {
		t.Fatalf("SetEnvironmentAutoStart(%s) failed: %v", mode, err)
	}
	return saved
}

func assertReturnedAutoStart(t *testing.T, got, want *bool, label string) {
	t.Helper()

	if !equalBoolPtr(got, want) {
		t.Fatalf("expected returned AutoStart=%s, got %+v", label, got)
	}
}

func assertStoredAutoStart(t *testing.T, got, want *bool, label string) {
	t.Helper()

	if !equalBoolPtr(got, want) {
		t.Fatalf("expected stored AutoStart=%s, got %+v", label, got)
	}
}

func equalBoolPtr(a, b *bool) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
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
				DefaultEnvironment: "dev",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev": {
				Name:               "dev",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-dev",
				CloudProviderAlias: "old-cloud",
				Type:               eruncommon.EnvironmentTypeRuntime,
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
		setRemoteCloudAlias: func(_ context.Context, endpoint, _, tenant, environment, alias string) (eruncommon.EnvConfig, error) {
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
				"frs": {Name: "frs", DefaultEnvironment: "prod"},
			},
			envs: map[string]eruncommon.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-prod",
					Type:              eruncommon.EnvironmentTypeRuntime,
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
	// The ERun tab runs a pure `erun open`: it no longer deploys (the shared thin
	// reconnect rebinds forwarders, deploy is the caller's job), so there is no
	// --skip-ensure flag any more.
	if got != "terminal open frs prod --app-session open-0" {
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
			"frs": {Name: "frs", DefaultEnvironment: "prod"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-prod",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
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
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: projectRoot, KubernetesContext: "rancher-desktop"},
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
				Type:         eruncommon.EnvironmentTypeRuntime,
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
// when the env became eligible. That behavior moved out of the desktop —
// the shared `MaybeArmOrFireIdleStop` in erun-common
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
				Type:               eruncommon.EnvironmentTypeRuntime,
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
				DefaultEnvironment: "dev-busy",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"team-busy/dev-busy": {
				Name:               "dev-busy",
				LocalRepoPath:      projectRoot,
				KubernetesContext:  "cluster-cloud",
				CloudProviderAlias: "team-cloud",
				ManagedCloud:       true,
				Type:               eruncommon.EnvironmentTypeRuntime,
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
		loadIdleStatus: func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
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

func TestSavePastedFileCopiesIntoCurrentRuntime(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {
				Name:               "erun",
				DefaultEnvironment: "local",
			},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "rancher-desktop",
			},
		},
	}

	fileData := []byte("pdf-data")
	var saved pastedFileSaveParams
	app := NewApp(erunUIDeps{
		store: store,
		savePastedFile: func(params pastedFileSaveParams) (string, error) {
			saved = params
			return "/home/erun/.codex/attachments/paste-report.pdf", nil
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

	// A non-image file (PDF) must be accepted and copied, not silently dropped.
	result, err := app.SavePastedFile(serial, pastedFilePayload{
		Data:     base64.StdEncoding.EncodeToString(fileData),
		MIMEType: "application/pdf",
		Name:     "report.pdf",
	})
	if err != nil {
		t.Fatalf("SavePastedFile failed: %v", err)
	}
	if result.Path != "/home/erun/.codex/attachments/paste-report.pdf" {
		t.Fatalf("unexpected pasted file path: %q", result.Path)
	}
	if string(saved.Data) != string(fileData) {
		t.Fatalf("unexpected saved data: %q", string(saved.Data))
	}
	if saved.MIMEType != "application/pdf" || saved.Name != "report.pdf" {
		t.Fatalf("unexpected saved metadata: %+v", saved)
	}
	if saved.Result.Tenant != "erun" || saved.Result.Environment != "local" {
		t.Fatalf("unexpected resolved target: %+v", saved.Result)
	}
}

func TestListAgentOutputsResolvesEnvAndLists(t *testing.T) {
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "local"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/local": {Name: "local", LocalRepoPath: t.TempDir(), KubernetesContext: "rancher-desktop"},
		},
	}
	var gotResult eruncommon.OpenResult
	app := NewApp(erunUIDeps{
		store: store,
		listAgentOutputs: func(result eruncommon.OpenResult, _ eruncommon.RuntimeOutputsParams) (eruncommon.RuntimeOutputsListResult, error) {
			gotResult = result
			return eruncommon.RuntimeOutputsListResult{
				Dir:     "/home/erun/.erun/outputs",
				Total:   1,
				Entries: []eruncommon.OutputEntry{{Name: "report.pdf", Size: 10}},
			}, nil
		},
	})
	defer app.shutdown(context.Background())

	got, err := app.ListAgentOutputs(uiSelection{Tenant: "erun", Environment: "local"})
	if err != nil {
		t.Fatalf("ListAgentOutputs failed: %v", err)
	}
	if gotResult.Tenant != "erun" || gotResult.Environment != "local" {
		t.Fatalf("dep resolved the wrong env: %+v", gotResult)
	}
	if len(got.Entries) != 1 || got.Entries[0].Name != "report.pdf" {
		t.Fatalf("unexpected list result: %+v", got)
	}
}

func TestListAgentOutputsRequiresSelection(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())
	if _, err := app.ListAgentOutputs(uiSelection{}); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
	}
}

func TestDownloadAgentOutputRequiresSelection(t *testing.T) {
	app := NewApp(erunUIDeps{store: stubUIStore{}})
	defer app.shutdown(context.Background())
	if _, err := app.DownloadAgentOutput(uiSelection{}, "report.pdf"); err == nil {
		t.Fatal("expected an error when tenant/environment are missing")
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

func TestDecodePastedFilePayloadAcceptsDataURL(t *testing.T) {
	imageData := []byte("png-data")
	got, mimeType, err := decodePastedFilePayload(pastedFilePayload{
		Data: "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageData),
	})
	if err != nil {
		t.Fatalf("decodePastedFilePayload failed: %v", err)
	}
	if string(got) != string(imageData) {
		t.Fatalf("unexpected decoded data: %q", string(got))
	}
	if mimeType != "image/png" {
		t.Fatalf("unexpected mime type: %q", mimeType)
	}
}

func TestDecodePastedFilePayloadAcceptsNonImage(t *testing.T) {
	// The image-only MIME gate is gone: a PDF (and a file with no MIME type at
	// all, common for arbitrary clipboard files) must decode without the old
	// "clipboard item is not an image" rejection.
	pdfData := []byte("%PDF-1.7 body")
	cases := []struct {
		name     string
		payload  pastedFilePayload
		wantMIME string
	}{
		{
			name:     "pdf_data_url",
			payload:  pastedFilePayload{Data: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(pdfData)},
			wantMIME: "application/pdf",
		},
		{
			name:     "empty_mime",
			payload:  pastedFilePayload{Data: base64.StdEncoding.EncodeToString(pdfData)},
			wantMIME: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, mimeType, err := decodePastedFilePayload(tc.payload)
			if err != nil {
				t.Fatalf("decodePastedFilePayload failed: %v", err)
			}
			if string(got) != string(pdfData) {
				t.Fatalf("unexpected decoded data: %q", string(got))
			}
			if mimeType != tc.wantMIME {
				t.Fatalf("unexpected mime type: %q", mimeType)
			}
		})
	}
}

func TestPastedFileFilenameDerivation(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)
	stamp := "paste-20260102-030405.600000000"
	cases := []struct {
		name     string
		mimeType string
		fileName string
		want     string
	}{
		{name: "preserves_pdf_name", mimeType: "application/pdf", fileName: "report.pdf", want: stamp + "-report.pdf"},
		{name: "preserves_csv_name", mimeType: "text/csv", fileName: "data.csv", want: stamp + "-data.csv"},
		{name: "preserves_extensionless_name", mimeType: "", fileName: "Makefile", want: stamp + "-Makefile"},
		{name: "image_name_preserved", mimeType: "image/png", fileName: "screenshot.png", want: stamp + "-screenshot.png"},
		{name: "no_name_known_mime_keeps_extension", mimeType: "image/png", fileName: "", want: stamp + ".png"},
		{name: "no_name_unknown_mime_falls_back_to_bin", mimeType: "", fileName: "", want: stamp + ".bin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pastedFileFilename(now, tc.mimeType, tc.fileName)
			if got != tc.want {
				t.Fatalf("pastedFileFilename(%q, %q) = %q, want %q", tc.mimeType, tc.fileName, got, tc.want)
			}
		})
	}
}

func TestPastedFileFilenameRejectsTraversal(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 600000000, time.UTC)
	// A crafted clipboard name must not let the staged file escape the
	// attachments dir. The derived filename must contain no path separator and
	// must not be "."/".." — so path.Join(dir, name) can never climb out.
	cases := []string{
		"../../etc/passwd",
		"/etc/passwd",
		"..\\..\\windows\\system32\\evil.dll",
		"sub/dir/evil.sh",
		"with\nnewline.txt",
		"..",
		".",
		"",
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			got := pastedFileFilename(now, "application/octet-stream", name)
			segment := strings.TrimPrefix(got, "paste-20260102-030405.600000000")
			if strings.ContainsAny(segment, "/\\") {
				t.Fatalf("derived filename %q contains a path separator for input %q", got, name)
			}
			joined := pastedFileRemoteDir() + "/" + got
			if !strings.HasPrefix(joined, pastedFileRemoteDir()+"/paste-") {
				t.Fatalf("derived path %q escaped the staging dir for input %q", joined, name)
			}
		})
	}
}

func TestBuildPastedFileCopyCommandTargetsRuntimeDeployment(t *testing.T) {
	// Use a non-erun tenant so the Helm release name (petios-devops) is distinct
	// from the runtime container literal (erun-devops). For tenant "erun" the two
	// coincide, which previously masked the bug where the release name was passed
	// as the kubectl -c container flag.
	result := eruncommon.OpenResult{
		Tenant:      "petios",
		Environment: "local",
		RepoPath:    "/Users/example/git/petios",
		EnvConfig: eruncommon.EnvConfig{
			KubernetesContext: "rancher-desktop",
		},
	}

	remoteDir := pastedFileRemoteDir()
	if remoteDir != "/home/erun/.codex/attachments" {
		t.Fatalf("unexpected remote dir: %q", remoteDir)
	}

	name, args, script := buildPastedFileCopyCommand(result, remoteDir, remoteDir+"/paste.png")
	if name != "kubectl" {
		t.Fatalf("unexpected command name: %q", name)
	}
	wantArgs := []string{
		"--context", "rancher-desktop",
		"--namespace", "petios-local",
		"exec", "-i",
		"-c", "erun-devops",
		"deployment/petios-devops",
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

func (s stubUIStore) SaveProjectConfig(projectRoot string, config eruncommon.ProjectConfig) error {
	if s.projectConfigs != nil {
		s.projectConfigs[projectRoot] = config
	}
	return nil
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
	resizes       [][2]int
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

func (s *stubTerminalSession) Resize(cols, rows int) error {
	s.mu.Lock()
	s.resizes = append(s.resizes, [2]int{cols, rows})
	s.mu.Unlock()
	return nil
}

func (s *stubTerminalSession) Resizes() [][2]int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([][2]int(nil), s.resizes...)
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
	// The AI tab runs a pure `erun open --ai`: claude launches pod-side (once on
	// create), so a reopen reconnects to the running tool and the desktop pipes no
	// initial input. open is pure — no --skip-ensure — so deploy stays the
	// caller's job.
	wantArgs := []string{"open", "erun", "remote", "--app-session", "ai", "--ai"}
	if strings.Join(started.Args, "\n") != strings.Join(wantArgs, "\n") {
		t.Fatalf("unexpected args: got %+v want %+v", started.Args, wantArgs)
	}
	if len(started.InitialInput) != 0 {
		t.Fatalf("AI tab must not pipe an initial input (claude launches pod-side), got %q", string(started.InitialInput))
	}
}

func swapAIRepaintNudgeTimings(delay, settle time.Duration) func() {
	prevDelay, prevSettle := aiRepaintNudgeDelay, aiRepaintNudgeSettle
	aiRepaintNudgeDelay, aiRepaintNudgeSettle = delay, settle
	return func() { aiRepaintNudgeDelay, aiRepaintNudgeSettle = prevDelay, prevSettle }
}

// TestAISessionRepaintNudgeFiresOnAttachMarker pins the fix: an AI tab
// reattaching to a running Claude renders blank because Claude (a main-screen
// TUI) only repaints on a real geometry change, and a same-size reattach
// raises none. The desktop forces the repaint by briefly resizing the pty one
// row shorter then back. The attach is detected by the bootstrap's window-title
// escape (OSC 0), emitted right before `dtach -A` — nudging on the first output
// overall would fire too early (it is the `erun open` audit trace). Verified
// end-to-end against a live pod via the headless app + DOM read; this locks the
// backend contract.
func TestAISessionRepaintNudgeFiresOnAttachMarker(t *testing.T) {
	defer swapAIRepaintNudgeTimings(time.Millisecond, time.Millisecond)()

	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{"erun": {Name: "erun", DefaultEnvironment: "remote"}},
		envs:    map[string]eruncommon.EnvConfig{"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"}},
	}
	session := newStubTerminalSession()
	app := NewApp(erunUIDeps{
		store:           store,
		findProjectRoot: func() (string, string, error) { return "erun", projectRoot, nil },
		resolveCLIPath:  func() string { return "/tmp/erun" },
		startTerminal:   func(startTerminalSessionParams) (terminalSession, error) { return session, nil },
	})
	defer app.shutdown(context.Background())

	managed := &managedTerminal{
		session:   session,
		selection: uiSelection{Tenant: "erun", Environment: "remote"},
		key:       aiSessionKey(uiSelection{Tenant: "erun", Environment: "remote"}, 0),
		serial:    1,
		kind:      sessionKindAI,
		lastCols:  80,
		lastRows:  24,
	}
	app.mu.Lock()
	app.sessions[managed.key] = managed
	app.mu.Unlock()

	var lastActivity time.Time
	// Output without the attach marker (the open audit trace) must not nudge.
	app.handleSessionOutput(managed, []byte("audit: erun open --ai --app-session ai erun remote\n"), &lastActivity)
	time.Sleep(20 * time.Millisecond)
	if got := session.Resizes(); len(got) != 0 {
		t.Fatalf("non-marker output must not nudge; got resizes %v", got)
	}

	// The attach marker (OSC 0 window-title set) triggers shrink-then-restore.
	app.handleSessionOutput(managed, []byte("\x1b]0;erun-remote\x07"), &lastActivity)
	want := [][2]int{{80, 23}, {80, 24}}
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := session.Resizes()
		if len(got) >= 2 {
			if got[0] != want[0] || got[1] != want[1] {
				t.Fatalf("unexpected nudge resizes: got %v want %v", got, want)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("repaint nudge did not fire; resizes=%v", got)
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The nudge is once per attach: a second marker chunk must not re-fire it.
	app.handleSessionOutput(managed, []byte("\x1b]0;erun-remote\x07"), &lastActivity)
	time.Sleep(30 * time.Millisecond)
	if got := session.Resizes(); len(got) != 2 {
		t.Fatalf("nudge must fire once per attach; got %v", got)
	}
}

func TestStartSessionAutoReconnectsOnExit(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
				ManagedCloud:      true,
			},
		},
	}
	app := NewApp(erunUIDeps{
		store:               store,
		canConnectLocalPort: func(int) bool { return false },
		loadIdleStatus: func(context.Context, string, string) (eruncommon.EnvironmentIdleStatus, error) {
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {
				Name:              "remote",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "cluster-cloud",
				Type:              eruncommon.EnvironmentTypeRuntime,
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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

	if _, err := app.StartSession(uiSelection{Tenant: "erun", Environment: "remote"}, 0, 80, 24); err != nil {
		t.Fatalf("StartSession failed: %v", err)
	}
	// Should not crash. No assertion required beyond that.
}

func TestStartLocalSessionDoesNotAutoReconnect(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		tenants: map[string]eruncommon.TenantConfig{
			"erun": {Name: "erun", DefaultEnvironment: "remote"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"erun/remote": {Name: "remote", LocalRepoPath: projectRoot, KubernetesContext: "ctx"},
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
