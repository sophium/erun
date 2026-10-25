package main

import (
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func nodeTestApp(t *testing.T, contextStatus string, envs map[string]configpkg.EnvConfig) *App {
	t.Helper()
	config := configpkg.ERunConfig{
		CloudContexts: []model.CloudContextConfig{{
			Name:               "erun-node-1",
			Provider:           "aws",
			CloudProviderAlias: "acct",
			KubernetesContext:  "erun-node-1-eu-west-2",
		}},
	}
	stored := make(map[string]configpkg.EnvConfig, len(envs))
	for name, env := range envs {
		env.Name = name
		stored["petios/"+name] = env
	}
	app := &App{}
	app.deps.store = stubUIStore{
		config:  &config,
		tenants: map[string]model.TenantConfig{"petios": {Name: "petios"}},
		envs:    stored,
	}
	if contextStatus != "" {
		app.cloudContextStatuses = map[string]cloudContextCacheEntry{
			"erun-node-1": {status: contextStatus, confirmedAt: time.Now()},
		}
	}
	return app
}

func linkedEnv() configpkg.EnvConfig {
	return configpkg.EnvConfig{CloudProviderAlias: "acct", KubernetesContext: "erun-node-1-eu-west-2"}
}

func TestEnvironmentNodeSnapshotReportsAStoppedNode(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil {
		t.Fatal("an env linked to a configured cloud context must resolve a node")
	}
	if node.Status != cloud.CloudContextStatusStopped {
		t.Fatalf("want the cached stopped status, got %q", node.Status)
	}
	if node.Name != "erun-node-1" || node.Label != "erun-node-1-eu-west-2" {
		t.Fatalf("the node must be named by its own identity, got name=%q label=%q", node.Name, node.Label)
	}
}

func TestEnvironmentNodeSnapshotReportsARunningNode(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusRunning, map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil || node.Status != cloud.CloudContextStatusRunning {
		t.Fatalf("want the cached running status, got %+v", node)
	}
}

func TestEnvironmentNodeSnapshotIsAbsentForAnEnvWithNoKubernetesContext(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"local": {}})
	if node := app.environmentNodeSnapshot("", ""); node != nil {
		t.Fatalf("an env with no kubernetes context must report no node, got %+v", node)
	}
}

func TestEnvironmentNodeSnapshotIsAbsentForAnUnmanagedCluster(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"k3d": {KubernetesContext: "k3d-local"}})
	if node := app.environmentNodeSnapshot("", "k3d-local"); node != nil {
		t.Fatalf("an env on an unmanaged cluster must report no node, got %+v", node)
	}
}

func TestEnvironmentNodeSnapshotCarriesAnUnobservedNodeAsUnknownNotStopped(t *testing.T) {
	app := nodeTestApp(t, "", map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil {
		t.Fatal("a linked node must still be reported when its status is unobserved")
	}
	if node.Status != "" {
		t.Fatalf("an unobserved node must carry no status, got %q", node.Status)
	}
}

func TestEnvironmentNodeSnapshotCarriesAStaleReadingAsUnknown(t *testing.T) {
	app := nodeTestApp(t, "", map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	app.cloudContextStatuses = map[string]cloudContextCacheEntry{
		"erun-node-1": {
			status:      cloud.CloudContextStatusRunning,
			confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL),
		},
	}
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil || node.Status != cloud.CloudContextStatusUnknown {
		t.Fatalf("a stale reading must read as unknown, got %+v", node)
	}
}

func collectEnvNodeEvents(app *App) *[]envNodePayload {
	events := make([]envNodePayload, 0)
	app.SetEmitter(func(name string, args ...any) {
		if name != uimodel.EnvNodeEvent || len(args) != 1 {
			return
		}
		if payload, ok := args[0].(envNodePayload); ok {
			events = append(events, payload)
		}
	})
	return &events
}

func TestRefreshEnvironmentNodeStatusesPublishesEachEnvironment(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{
		"develop": linkedEnv(),
		"local":   {},
	})
	events := collectEnvNodeEvents(app)
	app.refreshEnvironmentNodeStatuses()
	byEnv := make(map[string]envNodePayload, len(*events))
	for _, event := range *events {
		byEnv[event.Environment] = event
	}
	develop, ok := byEnv["develop"]
	if !ok {
		t.Fatal("the linked env must be published")
	}
	if develop.Node == nil || develop.Node.Status != cloud.CloudContextStatusStopped {
		t.Fatalf("want a stopped node for the linked env, got %+v", develop.Node)
	}
	local, ok := byEnv["local"]
	if !ok {
		t.Fatal("an env with no node must still be published, so the row can stop waiting for one")
	}
	if local.Node != nil {
		t.Fatalf("an env with no node must publish a nil node, got %+v", local.Node)
	}
}

func TestRefreshEnvironmentNodeStatusesRepublishesOnlyOnChange(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	events := collectEnvNodeEvents(app)
	app.refreshEnvironmentNodeStatuses()
	first := len(*events)
	app.refreshEnvironmentNodeStatuses()
	if len(*events) != first {
		t.Fatalf("an unchanged node reading must not re-publish: %d then %d events", first, len(*events))
	}
	app.applyCloudContextStatusesToCache([]model.CloudContextStatus{{
		CloudContextConfig: model.CloudContextConfig{Name: "erun-node-1"},
		Status:             cloud.CloudContextStatusRunning,
	}})
	app.refreshEnvironmentNodeStatuses()
	last := (*events)[len(*events)-1]
	if last.Node == nil || last.Node.Status != cloud.CloudContextStatusRunning {
		t.Fatalf("a changed node reading must publish, got %+v", last.Node)
	}
}

func TestStartingANodeRepublishesItsEnvironmentsImmediately(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	app.refreshEnvironmentNodeStatuses()
	events := collectEnvNodeEvents(app)
	app.setCloudContextStatusInCache("erun-node-1", cloud.CloudContextStatusRunning)
	if len(*events) == 0 {
		t.Fatal("a cache write from a start/stop handler must republish the envs on that node")
	}
	last := (*events)[len(*events)-1]
	if last.Node == nil || last.Node.Status != cloud.CloudContextStatusRunning {
		t.Fatalf("want the just-caused running status, got %+v", last.Node)
	}
}

func TestLoadStateSeedsTheNodeReadingOntoEachEnvironment(t *testing.T) {
	app := nodeTestApp(t, cloud.CloudContextStatusStopped, map[string]configpkg.EnvConfig{"develop": linkedEnv()})
	state := uiState{Tenants: []uiTenant{{
		Name:         "petios",
		Environments: []uiEnvironment{{Name: "develop"}},
	}}}
	app.seedEnvironmentNodeSnapshots(&state, lifecycle.ListResult{Tenants: []lifecycle.ListTenantResult{{
		Name: "petios",
		Environments: []lifecycle.ListEnvironmentResult{{
			Name:               "develop",
			CloudProviderAlias: "acct",
			KubernetesContext:  "erun-node-1-eu-west-2",
		}},
	}}})
	node := state.Tenants[0].Environments[0].Node
	if node == nil || node.Status != cloud.CloudContextStatusStopped {
		t.Fatalf("want the cached stopped node seeded onto the row, got %+v", node)
	}
}
