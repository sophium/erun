package main

import "strings"

// uiEnvironmentInitializedPayload is fired after `==> Initialized
// <tenant>/<env>` is observed in any PTY trace. The frontend uses it
// to refresh the env list and open the newly-created environment.
type uiEnvironmentInitializedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

// emitEnvironmentInitialized notifies the frontend that an env was
// just bootstrapped successfully. See activity_queue_app.go for the
// trace-line matcher that drives this. See erun-ui/AGENTS.md
// § "Command Completion And State-Refresh Wiring" for the lifecycle
// rationale: commands piped into the shared Local shell do not exit
// when they finish, so PTY-exit hooks are not a viable signal.
func (a *App) emitEnvironmentInitialized(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(environmentInitializedEvent, uiEnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

// emitEnvironmentInitFailed notifies the frontend that an init
// observed via `==> Initialization failed` failed. The frontend shows
// an error toast and reverts the optimistic `state.selected` for the
// failed env so the sidebar placeholder is removed.
func (a *App) emitEnvironmentInitFailed(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(environmentInitFailedEvent, uiEnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

// emitEnvironmentsChanged notifies the frontend that the on-disk erun
// config changed (file watcher). The frontend reloads state but does
// not auto-open anything because the watcher does not know which
// selection mutated.
func (a *App) emitEnvironmentsChanged() {
	a.emitEvent(environmentsChangedEvent, struct{}{})
}
