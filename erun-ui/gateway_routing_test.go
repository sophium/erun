package main

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common"
	"github.com/sophium/erun/erun-common/base/config"
)

// stubGatewayRouting answers the cluster read from a table keyed by
// "<tenant>/<env>", holding the base URL each running pod uses.
func stubGatewayRouting(t *testing.T, deployed map[string]string, failing map[string]bool) *[]string {
	t.Helper()
	var mu sync.Mutex
	var asked []string
	previous := readGatewayRouting
	readGatewayRouting = func(_ context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (eruncommon.GatewayRouting, bool, error) {
		key := tenant + "/" + env.Name
		mu.Lock()
		asked = append(asked, key)
		mu.Unlock()
		if failing[key] {
			return eruncommon.GatewayRouting{}, false, errors.New("cluster unreachable")
		}
		url, found := deployed[key]
		return eruncommon.GatewayRouting{Deployed: url, Wanted: eruncommon.WantedGatewayEndpoint(env.Claude, catalog)}, found, nil
	}
	t.Cleanup(func() { readGatewayRouting = previous })
	return &asked
}

// TestLoadGatewayRoutingDriftNamesPodsStillOnASwitchedOffGateway is the
// operator's report: switching the gateway off in ERun settings left every pod
// on it, and nothing in the desktop said a redeploy was what applies it.
func TestLoadGatewayRoutingDriftNamesPodsStillOnASwitchedOffGateway(t *testing.T) {
	const gateway = "https://openrouter.ai/api"
	no := false
	asked := stubGatewayRouting(t, map[string]string{
		"erun/build": gateway, // still on the switched-off gateway
		"erun/ux":    gateway, // same
		"erun/code1": "",      // already off
	}, map[string]bool{"erun/code2": true})
	app := NewApp(erunUIDeps{store: stubUIStore{
		// Switched off the way the ERun settings dialog does it: the base URL is
		// cleared and the catalog's models stay.
		config:  &config.ERunConfig{OpenRouter: &config.OpenRouterConfig{Models: []config.OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash"}}}},
		tenants: map[string]config.TenantConfig{"erun": {Name: "erun"}},
		envs: map[string]config.EnvConfig{
			"erun/build": {Name: "build", KubernetesContext: "erun-build"},
			"erun/ux":    {Name: "ux", KubernetesContext: "erun", Claude: config.EnvironmentClaudeConfig{UseGateway: &no}},
			"erun/code1": {Name: "code1", KubernetesContext: "erun"},
			"erun/code2": {Name: "code2", KubernetesContext: "erun"},
			"erun/host":  {Name: "host", Type: config.EnvironmentTypeHost, KubernetesContext: "erun"},
			"erun/none":  {Name: "none"},
		},
	}})

	got, err := app.LoadGatewayRoutingDrift()
	if err != nil {
		t.Fatal(err)
	}
	want := []uiGatewayRouting{
		{Tenant: "erun", Environment: "build", Deployed: gateway},
		{Tenant: "erun", Environment: "ux", Deployed: gateway},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("drift = %+v, want %+v", got, want)
	}
	for _, key := range *asked {
		if key == "erun/host" || key == "erun/none" {
			t.Fatalf("asked a cluster about %s, which has no runtime pod to read", key)
		}
	}
}

func TestLoadGatewayRoutingDriftNamesPodsNotYetOnASwitchedOnGateway(t *testing.T) {
	const gateway = "https://openrouter.ai/api"
	stubGatewayRouting(t, map[string]string{"erun/build": ""}, nil)
	app := NewApp(erunUIDeps{store: stubUIStore{
		config:  &config.ERunConfig{OpenRouter: &config.OpenRouterConfig{BaseURL: gateway}},
		tenants: map[string]config.TenantConfig{"erun": {Name: "erun"}},
		envs:    map[string]config.EnvConfig{"erun/build": {Name: "build", KubernetesContext: "erun-build"}},
	}})

	got, err := app.LoadGatewayRoutingDrift()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Wanted != gateway || got[0].Deployed != "" {
		t.Fatalf("drift = %+v, want erun/build to move onto the gateway", got)
	}
}

func TestLoadEnvironmentGatewayRouting(t *testing.T) {
	const gateway = "https://openrouter.ai/api"
	stubGatewayRouting(t, map[string]string{"erun/build": gateway, "erun/ux": ""}, nil)
	app := NewApp(erunUIDeps{store: stubUIStore{
		config:  &config.ERunConfig{},
		tenants: map[string]config.TenantConfig{"erun": {Name: "erun"}},
		envs: map[string]config.EnvConfig{
			"erun/build": {Name: "build", KubernetesContext: "erun-build"},
			"erun/ux":    {Name: "ux", KubernetesContext: "erun"},
		},
	}})

	drifted, err := app.LoadEnvironmentGatewayRouting(uiSelection{Tenant: "erun", Environment: "build"})
	if err != nil || drifted.Environment != "build" || drifted.Deployed != gateway {
		t.Fatalf("build = %+v err=%v, want its pod reported as still on the gateway", drifted, err)
	}
	matching, err := app.LoadEnvironmentGatewayRouting(uiSelection{Tenant: "erun", Environment: "ux"})
	if err != nil || matching != (uiGatewayRouting{}) {
		t.Fatalf("ux = %+v err=%v, want the zero value for a pod that matches its config", matching, err)
	}
}
