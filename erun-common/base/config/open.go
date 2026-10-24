package config

import (
	"errors"
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

var ErrEnvironmentNotFound = errors.New("no such environment exists")

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
	// AppSession, when non-empty, makes the remote shell a persistent,
	// reattachable dtach session keyed by this id, so closing/reopening a tab
	// or a transient kubectl-exec drop reconnects to the still-running shell
	// (and the AI tab's claude keeps working in the pod) instead of spawning a
	// parallel one. Empty keeps the ephemeral bare `erun open` behaviour.
	AppSession string
	// AI / Contribute select the persistent session's create-time program, run
	// once on create so a reattach never re-runs it: Contribute enters the
	// contribute clone with its env; AI launches the AI tool.
	AI         bool
	Contribute bool
	AITool     string
	Claude     EnvironmentClaudeConfig
	// Gateway is the erun-level OpenRouter catalog, when the operator has
	// configured one, carried over from OpenResult.Gateway. The AI launch
	// resolves this environment's effective gateway through it (model, Remote
	// Control, context window), and so does the deploy diagnosis, to tell a
	// gateway-routed environment whose pod template never consumed the gateway
	// from a healthy one (runtime_agent_credentials.go).
	Gateway *OpenRouterConfig
	// RuntimeImage lets the AI session prelude advise on the erun-build-env
	// skill only when the env still runs the default published runtime image.
	RuntimeImage string
	// RuntimeVersion and RuntimePod are the env config's recorded runtime
	// deploy intent, threaded through so observe can diff them against what
	// the live helm release and running pods actually hold.
	RuntimeVersion string
	RuntimePod     RuntimePodResources
}

func RuntimeReleaseName(tenant string) string {
	tenant = strings.TrimSpace(tenant)
	if tenant == "" {
		return DevopsComponentName
	}
	return tenant + "-devops"
}

func KubectlTargetArgs(req ShellLaunchParams) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(req.KubernetesContext) != "" {
		args = append(args, "--context", req.KubernetesContext)
	}
	if strings.TrimSpace(req.Namespace) != "" {
		args = append(args, "--namespace", req.Namespace)
	}
	return args
}
