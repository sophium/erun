package main

import (
	"strings"
)

// intentional_stop.go records user-driven Stop intent so the
// per-terminal reconnect loop in tryReconnect / shouldRespawnForCloudContext
// does not re-run `erun open` after the user clicks the Power button.
//
// Why it exists: when the user clicks Stop, the desktop fires
// StopCloudContext → AWS StopInstances. The kubectl PTY then dies
// because the API server connection drops; the reconnect loop wakes
// up and would re-run `erun open`, whose CloudContextPreflight calls
// StartCloudContext and undoes the user's stop. shouldRespawnForCloudContext
// already blocks respawn when the cloud-context status is anything
// other than running/pending, but that check reads on-disk env config
// updated by the status poller on its own cadence — there is a wide
// race window between the AWS call returning and the on-disk status
// flipping to stopping. This file closes that window by recording the
// intent the moment the user clicks Stop and clearing it when the user
// (or a successful start path) explicitly resumes the env.
//
// The store keys selectionKey(uiSelection) values so the resolution
// happens once at mark time; consult sites read intentionalStops under
// a.mu without re-walking the env list.

// markIntentionalStopForCloudContext finds every env whose linked
// cloud context matches `name` and records a Stop intent for each.
// Called from StopCloudContext before the AWS call so the marker is
// in place by the time the kubectl session dies.
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

// clearIntentionalStopForCloudContext removes any recorded Stop intent
// for envs linked to `name`. Called from StartCloudContext after a
// successful start so a subsequent kubectl drop reconnects normally.
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

// isIntentionalStop reports whether the user has explicitly asked to
// stop the env behind `selection`. Reads the flag without consuming
// it: multiple sessions for the same env (ERun tab plus AI tab, for
// example) all hit the reconnect gate around the same time when the
// kubectl connection drops, and each must see the marker. Cleared
// only by an explicit start (clearIntentionalStopForCloudContext).
func (a *App) isIntentionalStop(selection uiSelection) bool {
	key := selectionKey(selection)
	a.mu.Lock()
	_, ok := a.intentionalStops[key]
	a.mu.Unlock()
	return ok
}

// selectionsLinkedToCloudContext returns selectionKey() values for
// every env whose linked cloud context is `name`. Shared by the
// mark/clear helpers above; mirrors the env walk in
// clearIdleStopsForCloudContext so the two latches stay in sync.
func (a *App) selectionsLinkedToCloudContext(name string) []string {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil
	}
	tenants, err := a.deps.store.ListTenantConfigs()
	if err != nil {
		return nil
	}
	keys := make([]string, 0)
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
			keys = append(keys, selectionKey(uiSelection{Tenant: tenant.Name, Environment: env.Name}))
		}
	}
	return keys
}
