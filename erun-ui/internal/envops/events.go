package envops

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

type EnvironmentInitializedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
}

func initEmittedKey(tenant, environment string) string {
	return strings.TrimSpace(tenant) + "\x00" + strings.TrimSpace(environment)
}

// ClearInitEmitted lets a retry or re-create fire the event again.
func (s *Service) ClearInitEmitted(tenant, environment string) {
	s.initEmittedMu.Lock()
	delete(s.initEmitted, initEmittedKey(tenant, environment))
	s.initEmittedMu.Unlock()
}

// EnvironmentInitialized fires at most once per env: a Windows ConPTY
// repaint re-sends the "==> Initialized" line and would loop create->deploy.
func (s *Service) EnvironmentInitialized(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	key := initEmittedKey(tenant, environment)
	s.initEmittedMu.Lock()
	if _, done := s.initEmitted[key]; done {
		s.initEmittedMu.Unlock()
		return
	}
	s.initEmitted[key] = struct{}{}
	s.initEmittedMu.Unlock()
	s.d.Emit(uimodel.EnvironmentInitializedEvent, EnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

func (s *Service) EnvironmentInitFailed(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.ClearInitEmitted(tenant, environment)
	s.d.Emit(uimodel.EnvironmentInitFailedEvent, EnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

// EnvironmentDeployed opens tabs on this signal so they never spawn against an undeployed runtime.
func (s *Service) EnvironmentDeployed(tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.d.Emit(uimodel.EnvironmentDeployedEvent, EnvironmentInitializedPayload{
		Tenant:      tenant,
		Environment: environment,
	})
}

type DoctorCompletedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

// DoctorCompleted is `erun doctor`'s only completion signal.
func (s *Service) DoctorCompleted(tenant, environment string, success bool, message string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.d.Emit(uimodel.DoctorCompletedEvent, DoctorCompletedPayload{
		Tenant:      tenant,
		Environment: environment,
		Success:     success,
		Message:     message,
	})
}

type SSHDInitCompletedPayload struct {
	Tenant      string `json:"tenant"`
	Environment string `json:"environment"`
	Success     bool   `json:"success"`
	Message     string `json:"message,omitempty"`
}

// SSHDInitCompleted is `erun sshd init`'s only completion signal; piped
// commands never produce a PTY exit.
func (s *Service) SSHDInitCompleted(tenant, environment string, success bool, message string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	s.d.Emit(uimodel.SSHDInitCompletedEvent, SSHDInitCompletedPayload{
		Tenant:      tenant,
		Environment: environment,
		Success:     success,
		Message:     message,
	})
}
