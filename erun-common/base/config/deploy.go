package config

import (
	"fmt"
	"strings"
	"time"
)

// DefaultHelmDeploymentTimeout is the fallback helm rollout timeout when an
// environment sets no deploy.timeout.
//
// It is the bound on a cold pull, and the pull is the whole of its job: every
// runtime chart renders `strategy: Recreate` (required by the RWO home and
// docker-state claims every chart mounts), so helm tears the previous pod down
// before the replacement is even scheduled, and until the new image is on the
// node the environment runs nothing at all. The pod watcher keeps waiting while
// a pull is in progress and aborts early on a real, non-pull failure, so this
// value is not a patience setting for a broken rollout -- it is the only bound
// on a legitimate one.
//
// The former 5m was sized for a ~1GB runtime image. Measured on the real
// artifact: ghcr.io/sophium/erun-devops is 4.78 GB on disk / 1.14 GB compressed
// and a cold, uncapped `docker pull` of 1.0.295 took 386s (6m26s) on a fast
// link -- longer than the whole deadline, before any container starts. A fleet
// rollout makes that worse, not better: four environments pulling the same
// image at once were what surfaced it. 15m covers a cold pull of that image
// with room for a contended link and a slower one, and stays well inside the
// env's own idle/stop timings.
const DefaultHelmDeploymentTimeout = "15m0s"

// EnvironmentDeployConfig carries per-environment deploy tuning persisted on
// EnvConfig's `deploy:` block.
type EnvironmentDeployConfig struct {
	// Timeout is this env's helm rollout timeout; empty falls back to
	// DefaultHelmDeploymentTimeout.
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Components is the operator's per-machine saved deploy selection for this
	// env — what `erun deploy` rolls out when no --components flag is passed.
	// Empty means no saved selection: deploy falls back to the repo plan, then
	// the runtime chart alone. Selection is opt-in-only: only named charts (plus
	// the runtime when named or when the selection is empty) deploy.
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

// Resolve returns the canonical rollout-timeout string, falling back to
// DefaultHelmDeploymentTimeout when empty. A malformed or non-positive duration
// is a hard error so a misconfigured environment fails the deploy loudly rather
// than silently revert to the default.
func (c EnvironmentDeployConfig) Resolve() (string, error) {
	timeout := strings.TrimSpace(c.Timeout)
	if timeout == "" {
		return DefaultHelmDeploymentTimeout, nil
	}
	duration, err := time.ParseDuration(timeout)
	if err != nil {
		return "", fmt.Errorf("invalid environment deploy timeout %q", timeout)
	}
	if duration <= 0 {
		return "", fmt.Errorf("environment deploy timeout must be greater than zero")
	}
	return duration.String(), nil
}

func resolveDeployKubernetesContext(environment, configured string, currentContext func() (string, error)) string {
	environment = strings.TrimSpace(environment)
	configured = strings.TrimSpace(configured)
	if environment != DefaultEnvironment || configured != "" || currentContext == nil {
		return configured
	}

	current, err := currentContext()
	if err != nil {
		return configured
	}
	current = strings.TrimSpace(current)
	if current == "" {
		return configured
	}
	return current
}
