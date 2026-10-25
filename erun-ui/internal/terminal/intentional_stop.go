package terminal

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The stop intent is latched at click time because on-disk status lags the AWS
// Stop call, and the reconnect loop would otherwise re-run `erun open`.

// Must run before the AWS Stop call drops the kubectl session.
func (s *Service) MarkIntentionalStopForCloudContext(name string) {
	keys := s.selectionsLinkedToCloudContext(name)
	if len(keys) == 0 {
		return
	}
	s.mu.Lock()
	for _, key := range keys {
		s.intentionalStops[key] = struct{}{}
	}
	s.mu.Unlock()
}

func (s *Service) ClearIntentionalStopForCloudContext(name string) {
	keys := s.selectionsLinkedToCloudContext(name)
	if len(keys) == 0 {
		return
	}
	s.mu.Lock()
	for _, key := range keys {
		delete(s.intentionalStops, key)
	}
	s.mu.Unlock()
}

// Not consumed: several sessions for one env each check it.
func (s *Service) IsIntentionalStop(selection uimodel.Selection) bool {
	key := uimodel.SelectionKey(selection)
	s.mu.Lock()
	_, ok := s.intentionalStops[key]
	s.mu.Unlock()
	return ok
}

// Mirrors ClearIdleStopsForCloudContext so the two latches stay in sync.
func (s *Service) selectionsLinkedToCloudContext(name string) []string {
	selections := s.selectionsForCloudContext(name)
	keys := make([]string, 0, len(selections))
	for _, sel := range selections {
		keys = append(keys, uimodel.SelectionKey(sel))
	}
	return keys
}

func (s *Service) selectionsForCloudContext(name string) []uimodel.Selection {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	tenants, err := s.d.Store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	selections := make([]uimodel.Selection, 0)
	for _, tenant := range tenants {
		envs, err := s.d.Store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			cloudContext, ok, err := s.d.LinkedCloudContext(env)
			if err != nil || !ok || strings.TrimSpace(cloudContext.Name) != name {
				continue
			}
			selections = append(selections, uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return selections
}
