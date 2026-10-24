package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/registry"
)

func TestLoadStateUsesTenantSpecificDeployableVersionSuggestions(t *testing.T) {
	projectRoot := t.TempDir()
	app := NewApp(erunUIDeps{
		store: stubUIStore{
			tenants: map[string]configpkg.TenantConfig{
				"frs": {
					Name:               "frs",
					DefaultEnvironment: "prod",
				},
			},
			envs: map[string]configpkg.EnvConfig{
				"frs/prod": {
					Name:              "prod",
					LocalRepoPath:     projectRoot,
					KubernetesContext: "cluster-prod",
				},
			},
		},
		findProjectRoot:  func() (string, string, error) { return "frs", projectRoot, nil },
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "frs-devops":
				return registry.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"},
					LatestStable:   "1.0.11",
					LatestSnapshot: "1.0.12-snapshot-20260414165809",
				}, nil
			case configpkg.DefaultRuntimeImageName:
				return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return registry.RuntimeRegistryVersions{}, nil
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
		resolveBuildInfo:     func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
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
	if suggestions[0].Label != "frs latest stable" || suggestions[0].Image != configpkg.DefaultContainerRegistry+"/frs-devops" || suggestions[3].Label != "ERun current" || suggestions[3].Image != configpkg.DefaultContainerRegistry+"/"+configpkg.DefaultRuntimeImageName {
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
			envs: map[string]configpkg.EnvConfig{
				"team/prod": {Name: "prod", RuntimeRegistry: customRegistry},
			},
		},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			switch repository {
			case "team-devops":
				tenantImageNamespace = namespace
				return registry.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.11", "1.0.12-snapshot-20260608120000"},
					LatestStable:   "1.0.11",
					LatestSnapshot: "1.0.12-snapshot-20260608120000",
				}, nil
			case configpkg.DefaultRuntimeImageName:
				if namespace != configpkg.DefaultContainerRegistry {
					t.Fatalf("ERun fallback image must use the default registry, got %s", namespace)
				}
				return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return registry.RuntimeRegistryVersions{}, nil
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
			envs: map[string]configpkg.EnvConfig{
				"team/prod": {
					Name: "prod",
					ContainerRegistries: configpkg.ContainerRegistries{
						{Registry: "ghcr.io/acme", Roles: []configpkg.RegistryRole{configpkg.RegistryRoleBuild, configpkg.RegistryRoleFrom}},
						{Registry: "registry.internal/acme", Roles: []configpkg.RegistryRole{configpkg.RegistryRoleTo, configpkg.RegistryRoleDeploy}},
					},
				},
			},
		},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if repository == "team-devops" {
				queried[namespace] = true
				return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository, Tags: []string{"1.0.11"}, LatestStable: "1.0.11"}, nil
			}
			return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
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

func missingTenantImageRegistry(t *testing.T) func(context.Context, string, string) (registry.RuntimeRegistryVersions, error) {
	t.Helper()

	return func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
		if namespace != configpkg.DefaultContainerRegistry {
			t.Fatalf("unexpected registry namespace: %s", namespace)
		}
		if repository == "frs-devops" {
			return registry.RuntimeRegistryVersions{
				Image:          namespace + "/" + repository,
				Tags:           []string{"1.0.11", "1.0.10", "1.0.12-snapshot-20260414165809"},
				LatestStable:   "1.0.11",
				LatestSnapshot: "1.0.12-snapshot-20260414165809",
			}, nil
		}
		if repository == configpkg.DefaultRuntimeImageName {
			return registry.RuntimeRegistryVersions{
				Image:        namespace + "/" + repository,
				Tags:         []string{"1.0.50", "1.0.49"},
				LatestStable: "1.0.50",
			}, nil
		}
		t.Fatalf("unexpected registry repository: %s", repository)
		return registry.RuntimeRegistryVersions{}, nil
	}
}

func TestLoadVersionSuggestionsDoesNotDuplicateDefaultRuntimeForErunTenant(t *testing.T) {
	var repositories []string
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			repositories = append(repositories, repository)
			if repository != configpkg.DefaultRuntimeImageName {
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return registry.RuntimeRegistryVersions{
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
	if strings.Join(repositories, "\n") != configpkg.DefaultRuntimeImageName {
		t.Fatalf("expected one registry lookup for default runtime image, got %+v", repositories)
	}
}

func TestLoadVersionSuggestionsFallsBackToDefaultRuntimeTagsWhenTenantImageMissing(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			switch repository {
			case "test-devops":
				return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository}, nil
			case configpkg.DefaultRuntimeImageName:
				return registry.RuntimeRegistryVersions{
					Image:          namespace + "/" + repository,
					Tags:           []string{"1.0.50", "1.0.49", "1.0.51-snapshot-20260414165809"},
					LatestStable:   "1.0.50",
					LatestSnapshot: "1.0.51-snapshot-20260414165809",
				}, nil
			default:
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return registry.RuntimeRegistryVersions{}, nil
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
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected registry namespace: %s", namespace)
			}
			if repository == "test-devops" {
				return registry.RuntimeRegistryVersions{
					Image:        namespace + "/" + repository,
					Tags:         []string{"1.0.48"},
					LatestStable: "1.0.48",
				}, nil
			}
			if repository != configpkg.DefaultRuntimeImageName {
				t.Fatalf("unexpected registry repository: %s", repository)
			}
			return registry.RuntimeRegistryVersions{
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
	if suggestions[0].Image != configpkg.DefaultContainerRegistry+"/test-devops" || suggestions[1].Image != configpkg.DefaultContainerRegistry+"/"+configpkg.DefaultRuntimeImageName {
		t.Fatalf("unexpected suggestion metadata: %+v", suggestions)
	}
}

func TestLoadVersionSuggestionsSurfacesAuthNoticeForPrivateImage(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if repository == "frs-devops" {
				return registry.RuntimeRegistryVersions{}, fmt.Errorf("ghcr tags request failed: 401 Unauthorized: %w", registry.ErrRegistryAuthRequired)
			}
			return registry.RuntimeRegistryVersions{
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
	if notice := result.Notices[0]; notice.Kind != "auth" || notice.Image != configpkg.DefaultContainerRegistry+"/frs-devops" {
		t.Fatalf("unexpected notice: %+v", notice)
	}
}

func TestLoadVersionSuggestionsMarksUnreachableRegistry(t *testing.T) {
	app := NewApp(erunUIDeps{
		store:            stubUIStore{},
		resolveBuildInfo: func() model.BuildInfo { return model.BuildInfo{Version: "1.0.50"} },
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (registry.RuntimeRegistryVersions, error) {
			if repository == "frs-devops" {
				return registry.RuntimeRegistryVersions{}, fmt.Errorf("dial tcp: connection refused")
			}
			return registry.RuntimeRegistryVersions{Image: namespace + "/" + repository, Tags: []string{"1.0.50"}, LatestStable: "1.0.50"}, nil
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
