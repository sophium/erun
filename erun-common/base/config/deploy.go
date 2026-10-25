package config

import (
	"fmt"
	"strings"
	"time"
)

// DefaultHelmDeploymentTimeout bounds a cold pull of the runtime image (~1.1 GB
// compressed); charts use Recreate, so the env runs nothing until the pull ends.
const DefaultHelmDeploymentTimeout = "15m0s"

// EnvironmentDeployConfig is EnvConfig's `deploy:` block.
type EnvironmentDeployConfig struct {
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	// Components is the saved per-machine deploy selection; empty falls back to the
	// repo plan, then the runtime chart alone.
	Components []string `yaml:"components,omitempty" json:"components,omitempty"`
}

// Resolve returns the rollout timeout; a malformed or non-positive value is an
// error rather than a silent fallback.
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
