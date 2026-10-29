package settings

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// GatewayRouting is an environment whose pod routes Claude Code somewhere
// its saved configuration no longer asks for.
type GatewayRouting struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	// Deployed is "" for Anthropic directly.
	Deployed string `json:"deployed"`
	Wanted   string `json:"wanted"`
}

// gatewayRoutingReadTimeout keeps one unreachable cluster from holding up the rest.
const gatewayRoutingReadTimeout = 20 * time.Second

const gatewayRoutingReadConcurrency = 6

// LoadGatewayRoutingDrift lists environments whose running pod still routes
// where it was last deployed, since a gateway change applies only on redeploy.
// Unreadable environments are omitted, not reported as drifted.
func (s *Service) LoadGatewayRoutingDrift() ([]GatewayRouting, error) {
	if s.d.Store == nil {
		return []GatewayRouting{}, nil
	}
	candidates, err := s.gatewayRoutingCandidates()
	if err != nil {
		return nil, err
	}
	catalog := s.ResolveOpenRouterConfig()
	results := make([]GatewayRouting, len(candidates))
	slots := make(chan struct{}, gatewayRoutingReadConcurrency)
	var wg sync.WaitGroup
	for i, c := range candidates {
		wg.Add(1)
		go func(i int, c gatewayRoutingCandidate) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			results[i] = s.readGatewayRoutingDrift(c.tenant, c.env, catalog)
		}(i, c)
	}
	wg.Wait()

	out := []GatewayRouting{}
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

func (s *Service) gatewayRoutingCandidates() ([]gatewayRoutingCandidate, error) {
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return nil, err
	}
	var candidates []gatewayRoutingCandidate
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
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
func (s *Service) readGatewayRoutingDrift(tenant string, env config.EnvConfig, catalog *config.OpenRouterConfig) GatewayRouting {
	parent := s.d.Context()
	ctx, cancel := context.WithTimeout(parent, gatewayRoutingReadTimeout)
	defer cancel()
	routing, found, err := s.d.ReadGatewayRouting(ctx, env, tenant, catalog)
	if err != nil || !found || !routing.Drifted() {
		return GatewayRouting{}
	}
	return GatewayRouting{Tenant: tenant, Environment: env.Name, Deployed: routing.Deployed, Wanted: routing.Wanted}
}

// LoadEnvironmentGatewayRouting is LoadGatewayRoutingDrift for one environment.
func (s *Service) LoadEnvironmentGatewayRouting(selection uimodel.Selection) (GatewayRouting, error) {
	if s.d.Store == nil {
		return GatewayRouting{}, nil
	}
	tenant, environment := strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)
	env, ok := s.loadGatewayRoutingReadableEnv(tenant, environment)
	if !ok {
		return GatewayRouting{}, nil
	}
	return s.readGatewayRoutingDrift(tenant, env, s.ResolveOpenRouterConfig()), nil
}

func (s *Service) loadGatewayRoutingReadableEnv(tenant, environment string) (config.EnvConfig, bool) {
	env, _, err := s.d.Store.LoadEnvConfig(tenant, environment)
	return env, err == nil && gatewayRoutingReadable(env)
}

func gatewayRoutingReadable(env config.EnvConfig) bool {
	return env.Type != config.EnvironmentTypeHost && strings.TrimSpace(env.KubernetesContext) != ""
}
