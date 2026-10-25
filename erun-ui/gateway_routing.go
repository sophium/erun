package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/lifecycle"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// uiGatewayRouting is an environment whose pod routes Claude Code somewhere
// its saved configuration no longer asks for.
type uiGatewayRouting struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	// Deployed is "" for Anthropic directly.
	Deployed string `json:"deployed"`
	Wanted   string `json:"wanted"`
}

// gatewayRoutingReadTimeout keeps one unreachable cluster from holding up the rest.
const gatewayRoutingReadTimeout = 20 * time.Second

const gatewayRoutingReadConcurrency = 6

// readGatewayRouting is a seam so no test reaches kubectl.
var readGatewayRouting = lifecycle.ReadGatewayRouting

// LoadGatewayRoutingDrift lists environments whose running pod still routes
// where it was last deployed, since a gateway change applies only on redeploy.
// Unreadable environments are omitted, not reported as drifted.
func (a *App) LoadGatewayRoutingDrift() ([]uiGatewayRouting, error) {
	if a.deps.store == nil {
		return []uiGatewayRouting{}, nil
	}
	candidates, err := a.gatewayRoutingCandidates()
	if err != nil {
		return nil, err
	}
	catalog := a.resolveOpenRouterConfig()
	results := make([]uiGatewayRouting, len(candidates))
	slots := make(chan struct{}, gatewayRoutingReadConcurrency)
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c gatewayRoutingCandidate) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = a.readGatewayRoutingDrift(c.tenant, c.env, catalog)
		}(i, c)
	}
	wg.Wait()

	out := []uiGatewayRouting{}
	for _, r := range results {
		if r.Environment != "" {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Tenant != out[j].Tenant {
			return out[i].Tenant < out[j].Tenant
		}
		return out[i].Environment < out[j].Environment
	})
	return out, nil
}

type gatewayRoutingCandidate struct {
	tenant string
	env    config.EnvConfig
}

func (a *App) gatewayRoutingCandidates() ([]gatewayRoutingCandidate, error) {
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	var candidates []gatewayRoutingCandidate
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			if gatewayRoutingReadable(env) {
				candidates = append(candidates, gatewayRoutingCandidate{tenant: tenant.Name, env: env})
			}
		}
	}
	return candidates, nil
}

// readGatewayRoutingDrift returns the zero value on a match or an unreadable pod.
func (a *App) readGatewayRoutingDrift(tenant string, env config.EnvConfig, catalog *config.OpenRouterConfig) uiGatewayRouting {
	parent := a.context()
	ctx, cancel := context.WithTimeout(parent, gatewayRoutingReadTimeout)
	defer cancel()
	routing, found, err := readGatewayRouting(ctx, env, tenant, catalog)
	if err != nil || !found || !routing.Drifted() {
		return uiGatewayRouting{}
	}
	return uiGatewayRouting{Tenant: tenant, Environment: env.Name, Deployed: routing.Deployed, Wanted: routing.Wanted}
}

// LoadEnvironmentGatewayRouting is LoadGatewayRoutingDrift for one environment.
func (a *App) LoadEnvironmentGatewayRouting(selection uimodel.Selection) (uiGatewayRouting, error) {
	if a.deps.store == nil {
		return uiGatewayRouting{}, nil
	}
	tenant, environment := strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)
	env, ok := a.loadGatewayRoutingReadableEnv(tenant, environment)
	if !ok {
		return uiGatewayRouting{}, nil
	}
	return a.readGatewayRoutingDrift(tenant, env, a.resolveOpenRouterConfig()), nil
}

func (a *App) loadGatewayRoutingReadableEnv(tenant, environment string) (config.EnvConfig, bool) {
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	return env, err == nil && gatewayRoutingReadable(env)
}

func gatewayRoutingReadable(env config.EnvConfig) bool {
	return env.Type != config.EnvironmentTypeHost && strings.TrimSpace(env.KubernetesContext) != ""
}
