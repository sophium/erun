package main

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

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
	a.emitEvent(uimodel.EnvironmentInitializedEvent, uiEnvironmentInitializedPayload{
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
	a.emitEvent(uimodel.EnvironmentInitFailedEvent, uiEnvironmentInitializedPayload{
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
	a.emitEvent(uimodel.EnvironmentDeployedEvent, uiEnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
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
	a.emitEvent(uimodel.DoctorCompletedEvent, uiDoctorCompletedPayload{
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
	a.emitEvent(uimodel.SSHDInitCompletedEvent, uiSSHDInitCompletedPayload{
		Tenant:      tenant,
		Environment: environment,
		Success:     success,
		Message:     message,
	})
}
