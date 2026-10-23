package config

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

type OpenStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	LoadTenantConfig(string) (TenantConfig, string, error)
	LoadEnvConfig(string, string) (EnvConfig, string, error)
}

type OpenResult struct {
	Tenant       string
	Environment  string
	TenantConfig TenantConfig
	EnvConfig    EnvConfig
	LocalPorts   EnvironmentLocalPorts
	RepoPath     string
	Title        string
	// ClusterPullRegistry, when set, is the concrete in-cluster pull host a
	// context-resolved cluster registry resolved to; deploy renders it as the
	// chart's containerRegistry in place of the on-disk DEPLOY entry. Empty for a
	// plain (non-cluster) registry, so those envs are unaffected.
	ClusterPullRegistry string
	// ClusterRegistryInsecure marks the resolved cluster registry as plain HTTP,
	// so deploy tells the in-pod dind daemon to trust it (an in-pod build pushes
	// to this non-loopback host, which dind would otherwise reject as needing TLS).
	ClusterRegistryInsecure bool
	// Gateway is the erun-level gateway catalog, resolved with the target so
	// every caller that launches or diagnoses the environment's AI session
	// reaches the same catalog instead of each reading it (or forgetting to).
	// Nil when no catalog is configured or the root config cannot be read.
	Gateway *OpenRouterConfig
}

func (r OpenResult) RemoteRepo() bool {
	return r.EnvConfig.RemoteWorktree()
}

func resolveEffectiveKubernetesContext(environment, configured string, listContexts func() ([]string, error), currentContext func() (string, error)) string {
	environment = strings.TrimSpace(environment)
	configured = strings.TrimSpace(configured)
	if configured == "" || environment != DefaultEnvironment {
		return configured
	}
	if listContexts == nil || currentContext == nil {
		return configured
	}

	contexts, err := listContexts()
	if err != nil {
		return configured
	}
	if containsTrimmedString(contexts, configured) {
		return configured
	}

	current, err := currentContext()
	if err != nil {
		return configured
	}
	current = strings.TrimSpace(current)
	if current == "" || !containsTrimmedString(contexts, current) {
		return configured
	}

	return current
}

func listKubernetesContextNames() ([]string, error) {
	output, err := core.Command("kubectl", "config", "get-contexts", "-o=name").Output()
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(output), "\n")
	contexts := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		contexts = append(contexts, line)
	}
	return contexts, nil
}

func currentKubernetesContextName() (string, error) {
	output, err := core.Command("kubectl", "config", "current-context").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func containsTrimmedString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
