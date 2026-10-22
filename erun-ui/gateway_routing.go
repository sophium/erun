package main

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// uiGatewayRouting is one environment whose running pod routes Claude Code
// somewhere its saved configuration no longer asks for.
type uiGatewayRouting struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	// Deployed is the gateway the running pod uses, "" for Anthropic directly.
	Deployed string `json:"deployed"`
	// Wanted is the gateway the saved configuration resolves to, "" for none.
	Wanted string `json:"wanted"`
}

// gatewayRoutingReadTimeout bounds one environment's read: an unreachable
// cluster must not hold up the answer for every other environment.
const gatewayRoutingReadTimeout = 20 * time.Second

// gatewayRoutingReadConcurrency bounds how many clusters are asked at once.
const gatewayRoutingReadConcurrency = 6

// readGatewayRouting is the cluster read, a seam so no test reaches kubectl.
var readGatewayRouting = eruncommon.ReadGatewayRouting

// LoadGatewayRoutingDrift lists the environments whose running pod still
// routes where it was last deployed to rather than where the saved
// configuration now says.
//
// The gateway is rendered into the runtime Deployment, so switching it off in
// ERun settings — or opting one environment out — changes nothing a pod does
// until that environment is redeployed. Without this the switch looked like it
// had no effect at all; with it the desktop names the environments and offers
// the redeploy. An environment that cannot be read (no runtime deployed, an
// unreachable cluster, a host environment with no pod) is left out rather than
// reported as drifted: nothing here can be claimed about it.
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
	env    eruncommon.EnvConfig
}

// gatewayRoutingCandidates lists every configured environment that has a
// runtime pod to read.
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

// readGatewayRoutingDrift reads one environment's running pod, returning the
// zero value when it matches its saved configuration or cannot be read.
func (a *App) readGatewayRoutingDrift(tenant string, env eruncommon.EnvConfig, catalog *eruncommon.OpenRouterConfig) uiGatewayRouting {
	parent := a.ctx
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithTimeout(parent, gatewayRoutingReadTimeout)
	defer cancel()
	routing, found, err := readGatewayRouting(ctx, env, tenant, catalog)
	if err != nil || !found || !routing.Drifted() {
		return uiGatewayRouting{}
	}
	return uiGatewayRouting{Tenant: tenant, Environment: env.Name, Deployed: routing.Deployed, Wanted: routing.Wanted}
}

// LoadEnvironmentGatewayRouting is LoadGatewayRoutingDrift for one environment,
// for the Manage dialog's pending-redeploy banner. It returns the zero value
// (no Environment) when the pod matches its saved configuration or cannot be
// read.
func (a *App) LoadEnvironmentGatewayRouting(selection uiSelection) (uiGatewayRouting, error) {
	if a.deps.store == nil {
		return uiGatewayRouting{}, nil
	}
	tenant, environment := strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)
	env, _, err := a.deps.store.LoadEnvConfig(tenant, environment)
	if err != nil || !gatewayRoutingReadable(env) {
		return uiGatewayRouting{}, nil
	}
	return a.readGatewayRoutingDrift(tenant, env, a.resolveOpenRouterConfig()), nil
}

// gatewayRoutingReadable is an environment with a runtime pod erun deploys: a
// host environment has none, and one without a cluster has nowhere to look.
func gatewayRoutingReadable(env eruncommon.EnvConfig) bool {
	return env.Type != eruncommon.EnvironmentTypeHost && strings.TrimSpace(env.KubernetesContext) != ""
}
