package main

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// nodeTestApp wires one tenant with the given environments against one cloud
// context, so a test can assert what the sidebar's node reading resolves to
// without a cloud round trip.
func nodeTestApp(t *testing.T, contextStatus string, envs map[string]eruncommon.EnvConfig) *App {
	t.Helper()
	config := eruncommon.ERunConfig{
		CloudContexts: []eruncommon.CloudContextConfig{{
			Name:               "erun-node-1",
			Provider:           "aws",
			CloudProviderAlias: "acct",
			KubernetesContext:  "erun-node-1-eu-west-2",
		}},
	}
	stored := make(map[string]eruncommon.EnvConfig, len(envs))
	for name, env := range envs {
		env.Name = name
		stored["petios/"+name] = env
	}
	app := &App{}
	app.deps.store = stubUIStore{
		config:  &config,
		tenants: map[string]eruncommon.TenantConfig{"petios": {Name: "petios"}},
		envs:    stored,
	}
	if contextStatus != "" {
		app.cloudContextStatuses = map[string]cloudContextCacheEntry{
			"erun-node-1": {status: contextStatus, confirmedAt: time.Now()},
		}
	}
	return app
}

func linkedEnv() eruncommon.EnvConfig {
	return eruncommon.EnvConfig{CloudProviderAlias: "acct", KubernetesContext: "erun-node-1-eu-west-2"}
}

// The whole point of this file: a stopped node is a definite, actionable fact
// the sidebar already had cached and never carried per environment.
func TestEnvironmentNodeSnapshotReportsAStoppedNode(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil {
		t.Fatal("an env linked to a configured cloud context must resolve a node")
	}
	if node.Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("want the cached stopped status, got %q", node.Status)
	}
	if node.Name != "erun-node-1" || node.Label != "erun-node-1-eu-west-2" {
		t.Fatalf("the node must be named by its own identity, got name=%q label=%q", node.Name, node.Label)
	}
}

func TestEnvironmentNodeSnapshotReportsARunningNode(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusRunning, map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil || node.Status != eruncommon.CloudContextStatusRunning {
		t.Fatalf("want the cached running status, got %+v", node)
	}
}

// An env with no kubernetes context has no node to report. Reporting one — with
// any status at all — would be the confident wrong answer this issue is about.
func TestEnvironmentNodeSnapshotIsAbsentForAnEnvWithNoKubernetesContext(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"local": {}})
	if node := app.environmentNodeSnapshot("", ""); node != nil {
		t.Fatalf("an env with no kubernetes context must report no node, got %+v", node)
	}
}

// A kubernetes context that is not one of the configured cloud contexts is a
// cluster erun does not power-manage; that is also "no node", not "stopped".
func TestEnvironmentNodeSnapshotIsAbsentForAnUnmanagedCluster(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"k3d": {KubernetesContext: "k3d-local"}})
	if node := app.environmentNodeSnapshot("", "k3d-local"); node != nil {
		t.Fatalf("an env on an unmanaged cluster must report no node, got %+v", node)
	}
}

// The two shapes of "we do not know" the cache distinguishes: never observed
// ("") and a known-good reading gone stale ("unknown"). Both must survive to
// the read model as-is — neither may become "stopped".
func TestEnvironmentNodeSnapshotCarriesAnUnobservedNodeAsUnknownNotStopped(t *testing.T) {
	app := nodeTestApp(t, "", map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil {
		t.Fatal("a linked node must still be reported when its status is unobserved")
	}
	if node.Status != "" {
		t.Fatalf("an unobserved node must carry no status, got %q", node.Status)
	}
}

func TestEnvironmentNodeSnapshotCarriesAStaleReadingAsUnknown(t *testing.T) {
	app := nodeTestApp(t, "", map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	app.cloudContextStatuses = map[string]cloudContextCacheEntry{
		"erun-node-1": {
			status:      eruncommon.CloudContextStatusRunning,
			confirmedAt: time.Now().Add(-2 * cloudContextStatusTTL),
		},
	}
	node := app.environmentNodeSnapshot("acct", "erun-node-1-eu-west-2")
	if node == nil || node.Status != eruncommon.CloudContextStatusUnknown {
		t.Fatalf("a stale reading must read as unknown, got %+v", node)
	}
}

func collectEnvNodeEvents(app *App) *[]envNodePayload {
	events := make([]envNodePayload, 0)
	app.SetEmitter(func(name string, args ...any) {
		if name != envNodeEvent || len(args) != 1 {
			return
		}
		if payload, ok := args[0].(envNodePayload); ok {
			events = append(events, payload)
		}
	})
	return &events
}

// The sweep is what makes the cached status reach a row at all; without it the
// status is measured, cached, and rendered nowhere.
func TestRefreshEnvironmentNodeStatusesPublishesEachEnvironment(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{
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
	if develop.Node == nil || develop.Node.Status != eruncommon.CloudContextStatusStopped {
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

// Same discipline as the activity sweep: an unchanged reading must not cross
// the bridge on every tick.
func TestRefreshEnvironmentNodeStatusesRepublishesOnlyOnChange(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	events := collectEnvNodeEvents(app)
	app.refreshEnvironmentNodeStatuses()
	first := len(*events)
	app.refreshEnvironmentNodeStatuses()
	if len(*events) != first {
		t.Fatalf("an unchanged node reading must not re-publish: %d then %d events", first, len(*events))
	}
	app.applyCloudContextStatusesToCache([]eruncommon.CloudContextStatus{{
		CloudContextConfig: eruncommon.CloudContextConfig{Name: "erun-node-1"},
		Status:             eruncommon.CloudContextStatusRunning,
	}})
	app.refreshEnvironmentNodeStatuses()
	last := (*events)[len(*events)-1]
	if last.Node == nil || last.Node.Status != eruncommon.CloudContextStatusRunning {
		t.Fatalf("a changed node reading must publish, got %+v", last.Node)
	}
}

// An operator who starts a node from the titlebar must not wait out a poll
// interval before the rows that share it stop saying stopped.
func TestStartingANodeRepublishesItsEnvironmentsImmediately(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	app.refreshEnvironmentNodeStatuses()
	events := collectEnvNodeEvents(app)
	app.setCloudContextStatusInCache("erun-node-1", eruncommon.CloudContextStatusRunning)
	if len(*events) == 0 {
		t.Fatal("a cache write from a start/stop handler must republish the envs on that node")
	}
	last := (*events)[len(*events)-1]
	if last.Node == nil || last.Node.Status != eruncommon.CloudContextStatusRunning {
		t.Fatalf("want the just-caused running status, got %+v", last.Node)
	}
}

// LoadState is the other half: a page reload has no transition to replay, so
// the initial read model has to carry the reading the poller already holds.
func TestLoadStateSeedsTheNodeReadingOntoEachEnvironment(t *testing.T) {
	app := nodeTestApp(t, eruncommon.CloudContextStatusStopped, map[string]eruncommon.EnvConfig{"develop": linkedEnv()})
	state := uiState{Tenants: []uiTenant{{
		Name:         "petios",
		Environments: []uiEnvironment{{Name: "develop"}},
	}}}
	app.seedEnvironmentNodeSnapshots(&state, eruncommon.ListResult{Tenants: []eruncommon.ListTenantResult{{
		Name: "petios",
		Environments: []eruncommon.ListEnvironmentResult{{
			Name:               "develop",
			CloudProviderAlias: "acct",
			KubernetesContext:  "erun-node-1-eu-west-2",
		}},
	}}})
	node := state.Tenants[0].Environments[0].Node
	if node == nil || node.Status != eruncommon.CloudContextStatusStopped {
		t.Fatalf("want the cached stopped node seeded onto the row, got %+v", node)
	}
}
