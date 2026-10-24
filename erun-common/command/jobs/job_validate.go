package jobs

import (
	"time"
)

// The validators below run before a caller picks where the work happens, so a
// malformed request is refused for what is wrong with it rather than for
// whatever the environment's edge happened to answer. A request that is invalid
// on its face is invalid whether or not the environment can be reached.

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
func ValidateEnvironmentJobAttach(params AttachEnvironmentJobParams) error {
	_, err := normalizeEnvironmentJobIdentity(params.Tenant, params.Environment, params.Name, params.ID)
	return err
}

// ValidateEnvironmentJobAwaitTimeout enforces the bounded-wait ceiling.
func ValidateEnvironmentJobAwaitTimeout(timeout time.Duration) error {
	_, err := normalizeEnvironmentJobAwaitTimeout(timeout)
	return err
}
