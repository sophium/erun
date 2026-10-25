package activity

import (
	"strings"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// finishDeployByTenantEnv finalizes by the tenant/env parsed from the trace line,
// falling back to the selection, since the observing tab may target another env.
func (s *Service) finishDeployByTenantEnv(selection uimodel.Selection, tenant, environment string, status Status, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		s.finishActivityTracking(selection, status, errMsg)
		return
	}
	entry, ok := s.queue.findActiveByCommand("deploy", tenant, environment)
	if !ok {
		return
	}
	if final, finished := s.queue.finish(entry.ID, status, errMsg); finished {
		s.unlockTerminalsForActivity(final)
		if status == StatusSucceeded {
			// A successful deploy clears any stale 'failed' flag on the row.
			s.emitEnvStatus(uimodel.Selection{Tenant: tenant, Environment: environment}, "")
		}
		if status == StatusSucceeded || status == StatusSkipped {
			// Releases the create→deploy→open gate only once the runtime exists.
			s.d.EnvironmentDeployed(tenant, environment)
		}
	}
}

// startInitFromTrace registers the umbrella init entry; init's helm step still
// gets its own deploy entry.
func (s *Service) startInitFromTrace(selection uimodel.Selection, tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := s.queue.findActiveByCommand("init", tenant, environment); ok {
		return
	}
	kubeContext := s.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := s.queue.start(Entry{
		Command:           "init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	s.RememberKubeContext(kubeContext)
	s.lockTerminalsForActivity(entry)
}

func (s *Service) finishInitByTenantEnv(tenant, environment string, status Status, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	entry, ok := s.queue.findActiveByCommand("init", tenant, environment)
	if !ok {
		return
	}
	if final, finished := s.queue.finish(entry.ID, status, errMsg); finished {
		s.unlockTerminalsForActivity(final)
	}
}

// startDoctorFromTrace never locks terminals: doctor prompts interactively in the
// terminal it runs in.
func (s *Service) startDoctorFromTrace(selection uimodel.Selection, tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := s.queue.findActiveByCommand("doctor", tenant, environment); ok {
		return
	}
	kubeContext := s.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := s.queue.start(Entry{
		Command:           "doctor",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "doctor " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	s.RememberKubeContext(kubeContext)
}

// finishDoctorByTenantEnv also records the last-run outcome the Manage dialog shows.
func (s *Service) finishDoctorByTenantEnv(tenant, environment string, status Status, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if entry, ok := s.queue.findActiveByCommand("doctor", tenant, environment); ok {
		s.queue.finish(entry.ID, status, errMsg)
	}
	s.d.DoctorCompleted(tenant, environment, status == StatusSucceeded, errMsg)
}

// startSSHDInitFromTrace locks terminals like init/deploy; it has no interactive prompts.
func (s *Service) startSSHDInitFromTrace(selection uimodel.Selection, tenant, environment string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := s.queue.findActiveByCommand("sshd-init", tenant, environment); ok {
		return
	}
	kubeContext := s.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := s.queue.start(Entry{
		Command:           "sshd-init",
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           "sshd init " + tenant + "/" + environment,
	})
	if !fresh {
		return
	}
	s.RememberKubeContext(kubeContext)
	s.lockTerminalsForActivity(entry)
}

// finishSSHDInitByTenantEnv also records the last-run outcome the Manage dialog shows.
func (s *Service) finishSSHDInitByTenantEnv(tenant, environment string, status Status, errMsg string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if tenant == "" || environment == "" {
		return
	}
	if entry, ok := s.queue.findActiveByCommand("sshd-init", tenant, environment); ok {
		if final, finished := s.queue.finish(entry.ID, status, errMsg); finished {
			s.unlockTerminalsForActivity(final)
		}
	}
	s.d.SSHDInitCompleted(tenant, environment, status == StatusSucceeded, errMsg)
}

// startDeployFromTrace is a no-op when an active entry exists, so poller and trace converge.
func (s *Service) startDeployFromTrace(selection uimodel.Selection, tenant, environment, release, version string) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	release = strings.TrimSpace(release)
	version = strings.TrimSpace(version)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := s.queue.findActiveByCommand("deploy", tenant, environment); ok {
		return
	}
	// An empty release means the runtime chart; other components are labeled by name.
	summary := "deploy " + tenant + "/" + environment
	if release == "" {
		release = releaseNameForTenant(tenant)
	} else {
		summary += " · " + release
	}
	kubeContext := s.resolveActivityKubeContext(selection, tenant, environment)
	entry, fresh := s.queue.start(Entry{
		Command:           "deploy",
		Tenant:            tenant,
		Environment:       environment,
		Version:           version,
		Release:           release,
		Namespace:         namespaceForTenantEnv(tenant, environment),
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           summary,
	})
	if !fresh {
		return
	}
	s.RememberKubeContext(kubeContext)
	s.lockTerminalsForActivity(entry)
	s.startStatusPoller(entry)
}

// startCommandFromTrace keys build/release/push on the selection, since their
// trace lines carry no tenant/env; no-op without one. It never locks: these run
// in the terminal the user is reading.
func (s *Service) startCommandFromTrace(selection uimodel.Selection, command string) {
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	if _, ok := s.queue.findActiveByCommand(command, tenant, environment); ok {
		return
	}
	kubeContext := s.resolveActivityKubeContext(selection, tenant, environment)
	if _, fresh := s.queue.start(Entry{
		Command:           command,
		Tenant:            tenant,
		Environment:       environment,
		KubernetesContext: kubeContext,
		Source:            "trace",
		Summary:           command + " " + tenant + "/" + environment,
	}); !fresh {
		return
	}
	s.RememberKubeContext(kubeContext)
}

// finishCommandBySelection's unlock is idempotent.
func (s *Service) finishCommandBySelection(selection uimodel.Selection, command string, status Status, errMsg string) {
	tenant := strings.TrimSpace(selection.Tenant)
	environment := strings.TrimSpace(selection.Environment)
	if tenant == "" || environment == "" {
		return
	}
	entry, ok := s.queue.findActiveByCommand(command, tenant, environment)
	if !ok {
		return
	}
	if final, finished := s.queue.finish(entry.ID, status, errMsg); finished {
		s.unlockTerminalsForActivity(final)
	}
}
