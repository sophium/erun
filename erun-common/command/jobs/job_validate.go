package jobs

import (
	"time"

	"github.com/sophium/erun/erun-common/base/model"
)

// Validators run before routing, so a malformed request is refused on its own
// terms rather than by whatever the edge answers.

// ValidateEnvironmentJobStart checks a start request's target, handle, and work.
func ValidateEnvironmentJobStart(params StartEnvironmentJobParams) error {
	if _, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, params.Name, params.ID); err != nil {
		return err
	}
	if _, _, err := resolveEnvironmentJobWork(params.Agent, params.Prompt, params.Command); err != nil {
		return err
	}
	_, err := normalizeEnvironmentJobEnv(params.Env)
	return err
}

// ValidateEnvironmentJobAttach checks an attach request's target and handle.
func ValidateEnvironmentJobAttach(params model.AttachEnvironmentJobParams) error {
	_, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, params.Name, params.ID)
	return err
}

// ValidateEnvironmentJobAwaitTimeout enforces the bounded-wait ceiling.
func ValidateEnvironmentJobAwaitTimeout(timeout time.Duration) error {
	_, err := normalizeEnvironmentJobAwaitTimeout(timeout)
	return err
}
