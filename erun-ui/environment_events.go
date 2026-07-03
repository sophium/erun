package main

import "strings"

type uiEnvironmentInitializedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

// Commands piped into the shared Local shell never produce a PTY exit, so init
// success is observed from a trace line and surfaced as this event rather than
// a PTY-exit hook.
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

// The create→deploy→open flow gates opening an env's tabs on this signal, so
// tabs never spawn against a runtime that is not yet deployed.
func (a *App) emitEnvironmentDeployed(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(environmentDeployedEvent, uiEnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

// A config-file change carries no selection, so unlike the lifecycle events the
// frontend reloads state but must not auto-open anything.
func (a *App) emitEnvironmentsChanged() {
	a.emitEvent(environmentsChangedEvent, struct{}{})
}
