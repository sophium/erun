package settings

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/lifecycle/gatewayrouting"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// stubGatewayRouting answers the cluster read from a "<tenant>/<env>" table.
func stubGatewayRouting(deployed map[string]string, failing map[string]bool) (func(context.Context, config.EnvConfig, string, *config.OpenRouterConfig) (gatewayrouting.GatewayRouting, bool, error), *[]string) {
	var mu sync.Mutex
	var asked []string
	read := func(_ context.Context, env config.EnvConfig, tenant string, catalog *config.OpenRouterConfig) (gatewayrouting.GatewayRouting, bool, error) {
		key := tenant + "/" + env.Name
		mu.Lock()
		asked = append(asked, key)
		mu.Unlock()
		if failing[key] {
			return gatewayrouting.GatewayRouting{}, false, errors.New("cluster unreachable")
		}
		url, found := deployed[key]
		return gatewayrouting.GatewayRouting{Deployed: url, Wanted: gatewayrouting.WantedGatewayEndpoint(env.Claude, catalog)}, found, nil
	}
	return read, &asked
}

func TestLoadGatewayRoutingDriftNamesPodsStillOnASwitchedOffGateway(t *testing.T) {
	const gateway = "https://openrouter.ai/api"
	no := false
	read, asked := stubGatewayRouting(map[string]string{
		"erun/build": gateway, // still on the switched-off gateway
		"erun/ux":    gateway, // same
		"erun/code1": "",      // already off
	}, map[string]bool{"erun/code2": true})
	svc := newTestService(Deps{ReadGatewayRouting: read, Store: fakeStore{
		// Switched off the way the settings dialog does: base URL cleared, models kept.
		config:  &config.ERunConfig{OpenRouter: &config.OpenRouterConfig{Models: []config.OpenRouterModel{{ID: "deepseek/deepseek-v4.1-flash"}}}},
		tenants: map[string]model.TenantConfig{"erun": {Name: "erun"}},
		envs: map[string]config.EnvConfig{
			"erun/build": {Name: "build", KubernetesContext: "erun-build"},
			"erun/ux":    {Name: "ux", KubernetesContext: "erun", Claude: config.EnvironmentClaudeConfig{UseGateway: &no}},
			"erun/code1": {Name: "code1", KubernetesContext: "erun"},
			"erun/code2": {Name: "code2", KubernetesContext: "erun"},
			"erun/host":  {Name: "host", Type: config.EnvironmentTypeHost, KubernetesContext: "erun"},
			"erun/none":  {Name: "none"},
		},
	}})

	got, err := svc.LoadGatewayRoutingDrift()
	if err != nil {
		t.Fatal(err)
	}
	want := []GatewayRouting{
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
	read, _ := stubGatewayRouting(map[string]string{"erun/build": ""}, nil)
	svc := newTestService(Deps{ReadGatewayRouting: read, Store: fakeStore{
		config:  &config.ERunConfig{OpenRouter: &config.OpenRouterConfig{BaseURL: gateway}},
		tenants: map[string]model.TenantConfig{"erun": {Name: "erun"}},
		envs:    map[string]config.EnvConfig{"erun/build": {Name: "build", KubernetesContext: "erun-build"}},
	}})

	got, err := svc.LoadGatewayRoutingDrift()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Wanted != gateway || got[0].Deployed != "" {
		t.Fatalf("drift = %+v, want erun/build to move onto the gateway", got)
	}
}

func TestLoadEnvironmentGatewayRouting(t *testing.T) {
	const gateway = "https://openrouter.ai/api"
	read, _ := stubGatewayRouting(map[string]string{"erun/build": gateway, "erun/ux": ""}, nil)
	svc := newTestService(Deps{ReadGatewayRouting: read, Store: fakeStore{
		config:  &config.ERunConfig{},
		tenants: map[string]model.TenantConfig{"erun": {Name: "erun"}},
		envs: map[string]config.EnvConfig{
			"erun/build": {Name: "build", KubernetesContext: "erun-build"},
			"erun/ux":    {Name: "ux", KubernetesContext: "erun"},
		},
	}})

	drifted, err := svc.LoadEnvironmentGatewayRouting(uimodel.Selection{Tenant: "erun", Environment: "build"})
	if err != nil || drifted.Environment != "build" || drifted.Deployed != gateway {
		t.Fatalf("build = %+v err=%v, want its pod reported as still on the gateway", drifted, err)
	}
	matching, err := svc.LoadEnvironmentGatewayRouting(uimodel.Selection{Tenant: "erun", Environment: "ux"})
	if err != nil || matching != (GatewayRouting{}) {
		t.Fatalf("ux = %+v err=%v, want the zero value for a pod that matches its config", matching, err)
	}
}
