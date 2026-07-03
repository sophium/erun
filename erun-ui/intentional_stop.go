package main

import (
	"strings"
)

// intentional_stop.go records user-driven Stop intent so the per-terminal
// reconnect loop does not re-run `erun open` — which would restart the
// instance and undo the stop — after the user clicks the Power button.
//
// Stopping the instance drops the kubectl session, which wakes the reconnect
// loop. The status-based respawn guard alone cannot suppress the respawn: it
// reads on-disk env status that lags the AWS Stop call, leaving a race window
// in which the loop still sees the env as running. Latching the intent the
// instant the user clicks Stop closes that window; an explicit resume clears it.

// markIntentionalStopForCloudContext must run before StopCloudContext's AWS
// Stop call, so the intent is latched before the kubectl session drops and the
// reconnect loop wakes.
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

// clearIntentionalStopForCloudContext clears the latch after a successful
// start, so a later kubectl drop reconnects instead of staying stopped.
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

// isIntentionalStop reads the flag without consuming it: several sessions for
// one env (an ERun tab plus an AI tab) hit the reconnect gate together when
// kubectl drops, and each must see the marker.
func (a *App) isIntentionalStop(selection uiSelection) bool {
	key := selectionKey(selection)
	a.mu.Lock()
	_, ok := a.intentionalStops[key]
	a.mu.Unlock()
	return ok
}

// selectionsLinkedToCloudContext mirrors the env walk in
// clearIdleStopsForCloudContext so the two latches stay in sync.
func (a *App) selectionsLinkedToCloudContext(name string) []string {
	selections := a.selectionsForCloudContext(name)
	keys := make([]string, 0, len(selections))
	for _, sel := range selections {
		keys = append(keys, selectionKey(sel))
	}
	return keys
}

func (a *App) selectionsForCloudContext(name string) []uiSelection {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	selections := make([]uiSelection, 0)
	for _, tenant := range tenants {
		envs, err := a.deps.store.ListEnvConfigs(tenant.Name)
		if err != nil {
			continue
		}
		for _, env := range envs {
			cloudContext, ok, err := a.linkedCloudContext(env)
			if err != nil || !ok || strings.TrimSpace(cloudContext.Name) != name {
				continue
			}
			selections = append(selections, uiSelection{Tenant: tenant.Name, Environment: env.Name})
		}
	}
	return selections
}
