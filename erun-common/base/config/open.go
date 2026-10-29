package config

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

type OpenStore interface {
	LoadERunConfig() (ERunConfig, string, error)
	LoadTenantConfig(string) (model.TenantConfig, string, error)
	LoadEnvConfig(string, string) (EnvConfig, string, error)
}

type OpenResult struct {
	Tenant       string
	Environment  string
	TenantConfig model.TenantConfig
	EnvConfig    EnvConfig
	LocalPorts   model.EnvironmentLocalPorts
	RepoPath     string
	Title        string
	// ClusterPullRegistry is the resolved in-cluster pull host deploy renders in
	// place of the DEPLOY entry; empty for a plain registry.
	ClusterPullRegistry string
	// ClusterRegistryInsecure tells in-pod dind to trust the plain-HTTP cluster registry.
	ClusterRegistryInsecure bool
	// Gateway is resolved once here so every AI launch and diagnosis sees the same
	// catalog; nil when none is configured or readable.
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

type ShellLaunchParams struct {
	Dir                string
	Tenant             string
	Environment        string
	Title              string
	Namespace          string
	KubernetesContext  string
	RemoteRepo         bool
	Type               EnvironmentType
	ManagedCloud       bool
	CloudProviderAlias string
	Idle               EnvironmentIdleConfig
	// AppSession, when set, makes the shell a reattachable dtach session keyed by
	// this id; empty keeps the ephemeral `erun open` shell.
	AppSession string
	// AI and Contribute pick the session's create-time program; a reattach never re-runs it.
	AI         bool
	Contribute bool
	AITool     string
	Claude     EnvironmentClaudeConfig
	// Gateway is OpenResult.Gateway, used by the AI launch and the deploy diagnosis.
	Gateway *OpenRouterConfig
	// RuntimeImage gates the erun-build-env advice to the default runtime image.
	RuntimeImage string
	// RuntimeVersion and RuntimePod are the recorded deploy intent observe diffs
	// against the live release.
	RuntimeVersion string
	RuntimePod     model.RuntimePodResources
}
