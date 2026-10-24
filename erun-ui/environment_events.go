package main

import "strings"

type uiEnvironmentInitializedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

func initEmittedKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "\x00" + strings.TrimSpace(environment)
}

// clearInitEmitted lets a retry or re-create fire the event again.
func (a *App) clearInitEmitted(tenant, environment string) {
	a.initEmittedMu.Lock()
	delete(a.initEmitted, initEmittedKey(tenant, environment))
	a.initEmittedMu.Unlock()
}

// emitEnvironmentInitialized fires at most once per env: a Windows ConPTY
// repaint re-sends the "==> Initialized" line and would loop create->deploy.
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
	a.clearInitEmitted(tenant, environment)
	a.emitEvent(environmentInitFailedEvent, uiEnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

// Tabs open on this signal so they never spawn against an undeployed runtime.
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

// A config-file change carries no selection, so the frontend must not auto-open.
func (a *App) emitEnvironmentsChanged() {
	a.emitEvent(environmentsChangedEvent, struct{}{})
}

type uiHostedDefinitionUploadedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Revision    int    `json:"revision"`
}

// emitHostedDefinitionUploaded refreshes the Manage dialog, which
// environments-changed does not reload.
func (a *App) emitHostedDefinitionUploaded(tenant, environment string, revision int) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	a.emitEvent(hostedDefinitionUploadedEvent, uiHostedDefinitionUploadedPayload{
		Tenant:      tenant,
		Environment: environment,
		Revision:    revision,
	})
}

type uiDoctorCompletedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

// emitDoctorCompleted is `erun doctor`'s only completion signal.
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

// emitSSHDInitCompleted is `erun sshd init`'s only completion signal; piped
// commands never produce a PTY exit.
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
