package main

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// The stop intent is latched at click time because on-disk status lags the AWS
// Stop call, and the reconnect loop would otherwise re-run `erun open`.

// Must run before the AWS Stop call drops the kubectl session.
func (a *App) markIntentionalStopForCloudContext(name string) {
	keys := a.selectionsLinkedToCloudContext(name)
	if len(keys) == 0 {
		return
	}
	a.mu.Lock()
	for _, key := range keys {
		a.intentionalStops[key] = struct{}{}
	}
	a.mu.Unlock()
}

func (a *App) clearIntentionalStopForCloudContext(name string) {
	keys := a.selectionsLinkedToCloudContext(name)
	if len(keys) == 0 {
		return
	}
	a.mu.Lock()
	for _, key := range keys {
		delete(a.intentionalStops, key)
	}
	a.mu.Unlock()
}

// Not consumed: several sessions for one env each check it.
func (a *App) isIntentionalStop(selection uimodel.Selection) bool {
	key := selectionKey(selection)
	a.mu.Lock()
	_, ok := a.intentionalStops[key]
	a.mu.Unlock()
	return ok
}

// Mirrors clearIdleStopsForCloudContext so the two latches stay in sync.
func (a *App) selectionsLinkedToCloudContext(name string) []string {
	selections := a.selectionsForCloudContext(name)
	keys := make([]string, 0, len(selections))
	for _, sel := range selections {
		keys = append(keys, selectionKey(sel))
	}
	return keys
}

func (a *App) selectionsForCloudContext(name string) []uimodel.Selection {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	selections := make([]uimodel.Selection, 0)
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			cloudContext, ok, err := a.settings.LinkedCloudContext(env)
			if err != nil || !ok || strings.TrimSpace(cloudContext.Name) != name {
				continue
			}
			selections = append(selections, uimodel.Selection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return selections
}
