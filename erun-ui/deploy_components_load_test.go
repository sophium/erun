package main

import (
	"context"
	"reflect"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	componentspkg "github.com/sophium/erun/erun-common/primitive/deploy/components"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// A local-agent env lists the charts published at the deploy version plus the
// runtime, never its working-tree chart directories.
func TestLoadDeployComponentsLocalAgentShowsPublishedVersionView(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/local": {
				Name:              "local",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              configpkg.EnvironmentTypeLocalAgent,
			},
		},
	}
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.0"},
		"charts/frs-backend-db":       {"1.0.0"},
		"charts/frs-backend-api":      {"1.0.0"},
		"charts/frs-powerdns":         {"1.0.0"},
		"charts/frs-docs":             {"1.0.0"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return model.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []componentspkg.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	at100, err := app.LoadDeployComponents(uimodel.Selection{Tenant: "frs", Environment: "local", Version: "1.0.0"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.0) failed: %v", err)
	}
	wantAt100 := []string{
		"frs-devops", "frs-backend-postgres", "frs-backend-db",
		"frs-backend-api", "frs-powerdns", "frs-docs",
	}
	if got := names(at100.Components); !reflect.DeepEqual(got, wantAt100) {
		t.Fatalf("components at 1.0.0 = %v, want %v (published-version view, runtime first)", got, wantAt100)
	}
	runtime := at100.Components[0]
	if !runtime.Runtime || !runtime.Selected || runtime.Source != "published-chart" {
		t.Fatalf("runtime item = %+v, want {frs-devops runtime selected published-chart}", runtime)
	}

	// No version-to-deploy: the current version (1.0.106) publishes no component charts.
	atCurrent, err := app.LoadDeployComponents(uimodel.Selection{Tenant: "frs", Environment: "local"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent.Components), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current 1.0.106 = %v, want %v (runtime only)", got, want)
	}
}

// The registry probe cannot be driven from headless Playwright, so filtering is asserted here.
func TestLoadDeployComponentsVersionAwareFiltersUnavailableCharts(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.106",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	chartTags := map[string][]string{
		"charts/frs-backend-postgres": {"1.0.112"},
		"charts/frs-backend-api":      {"1.0.112"},
	}
	app := NewApp(erunUIDeps{
		store: store,
		resolveImageRegistry: func(_ context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
			if namespace != configpkg.DefaultContainerRegistry {
				t.Fatalf("unexpected chart registry namespace: %s", namespace)
			}
			return model.RuntimeRegistryVersions{Tags: chartTags[repository]}, nil
		},
	})

	names := func(components []componentspkg.DeployableComponent) []string {
		out := make([]string, 0, len(components))
		for _, component := range components {
			out = append(out, component.Name)
		}
		return out
	}

	at112, err := app.LoadDeployComponents(uimodel.Selection{Tenant: "frs", Environment: "prod", Version: "1.0.112"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(1.0.112) failed: %v", err)
	}
	if got, want := names(at112.Components), []string{"frs-devops", "frs-backend-postgres", "frs-backend-api"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at 1.0.112 = %v, want %v (runtime first; unpublished db/powerdns/docs filtered out)", got, want)
	}

	atCurrent, err := app.LoadDeployComponents(uimodel.Selection{Tenant: "frs", Environment: "prod"})
	if err != nil {
		t.Fatalf("LoadDeployComponents(current) failed: %v", err)
	}
	if got, want := names(atCurrent.Components), []string{"frs-devops"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("components at current version 1.0.106 = %v, want %v (runtime only)", got, want)
	}
}

// The runtime row must name the chart resolvePublishedRuntimeChartReference installs.
func TestLoadDeployComponentsRuntimeChartReflectsTenantChart(t *testing.T) {
	projectRoot := t.TempDir()
	store := stubUIStore{
		config:  &configpkg.ERunConfig{},
		tenants: map[string]model.TenantConfig{"frs": {Name: "frs"}},
		envs: map[string]configpkg.EnvConfig{
			"frs/prod": {
				Name:              "prod",
				LocalRepoPath:     projectRoot,
				KubernetesContext: "test-context",
				RuntimeVersion:    "1.0.20",
				Type:              configpkg.EnvironmentTypeRuntime,
			},
		},
	}
	runtimePublishedChart := func(t *testing.T, tags map[string][]string) string {
		t.Helper()
		app := NewApp(erunUIDeps{
			store: store,
			resolveImageRegistry: func(_ context.Context, _, repository string) (model.RuntimeRegistryVersions, error) {
				return model.RuntimeRegistryVersions{Tags: tags[repository]}, nil
			},
		})
		components, err := app.LoadDeployComponents(uimodel.Selection{Tenant: "frs", Environment: "prod", Version: "1.0.20"})
		if err != nil {
			t.Fatalf("LoadDeployComponents failed: %v", err)
		}
		for _, component := range components.Components {
			if component.Runtime {
				return component.PublishedChart
			}
		}
		t.Fatal("no runtime component returned")
		return ""
	}

	if got := runtimePublishedChart(t, map[string][]string{"charts/frs-devops": {"1.0.20"}}); got != "frs-devops" {
		t.Fatalf("runtime PublishedChart with tenant chart published = %q, want frs-devops", got)
	}
	if got := runtimePublishedChart(t, map[string][]string{}); got != "erun-devops" {
		t.Fatalf("runtime PublishedChart with no tenant chart = %q, want erun-devops (fallback)", got)
	}
}
