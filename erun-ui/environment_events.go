package main

import "strings"

type uiEnvironmentInitializedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

func initEmittedKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "\x00" + strings.TrimSpace(environment)
}

// clearInitEmitted lets a later legitimate (re-)initialization of the same env
// fire the event again — after an init failure (retry) or a delete + re-create.
func (a *App) clearInitEmitted(tenant, environment string) {
	a.initEmittedMu.Lock()
	delete(a.initEmitted, initEmittedKey(tenant, environment))
	a.initEmittedMu.Unlock()
}

// Commands piped into the shared Local shell never produce a PTY exit, so init
// success is observed from a trace line and surfaced as this event rather than
// a PTY-exit hook. It fires AT MOST ONCE per env: a Windows ConPTY repaint (from
// writing the follow-up deploy command into the same Local shell) re-sends the
// buffered "==> Initialized" line as fresh output, and without this guard each
// re-fire composed another deploy — whose write repainted again — an endless
// create→deploy loop. clearInitEmitted resets it for a retry/re-create.
func (a *App) emitEnvironmentInitialized(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	key := initEmittedKey(tenant, environment)
	a.initEmittedMu.Lock()
	if a.initEmitted == nil {
		a.initEmitted = make(map[string]struct{})
	}
	if _, done := a.initEmitted[key]; done {
		a.initEmittedMu.Unlock()
		return
	}
	a.initEmitted[key] = struct{}{}
	a.initEmittedMu.Unlock()
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
	// A failed init may be retried; allow the next success to fire the event.
	a.clearInitEmitted(tenant, environment)
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

type uiDoctorCompletedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

// emitDoctorCompleted is `erun doctor`'s only completion signal (see
// handleDoctorTraceLine): the Manage dialog's SSH tab records it as the
// persisted last-run outcome so "is this healthy?" has a visible answer.
func (a *App) emitDoctorCompleted(tenant, environment string, success bool, message string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(doctorCompletedEvent, uiDoctorCompletedPayload{
		Tenant:      tenant,
		Environment: environment,
		Success:     success,
		Message:     message,
	})
}

type uiSSHDInitCompletedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

// emitSSHDInitCompleted is `erun sshd init`'s only completion signal (see
// handleSSHDInitTraceLine): it runs piped into the shared Local shell like
// doctor, so the Manage dialog's SSH tab records this event as the persisted
// last-run outcome instead of relying on a PTY exit that never fires.
func (a *App) emitSSHDInitCompleted(tenant, environment string, success bool, message string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(sshdInitCompletedEvent, uiSSHDInitCompletedPayload{
		Tenant:      tenant,
		Environment: environment,
		Success:     success,
		Message:     message,
	})
}
