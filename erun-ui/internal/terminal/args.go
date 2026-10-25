package terminal

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func buildSSHDInitArgs(selection uimodel.Selection) []string {
	return []string{"sshd", "init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

func buildDoctorArgs(selection uimodel.Selection) []string {
	return []string{"doctor", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

// buildOpenNoShellArgs passes --reconnect: a rebind must never start an environment the operator stopped.
func buildOpenNoShellArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment), "--no-shell", "--no-alias-prompt", "--reconnect"}
}

func buildInitArgs(selection uimodel.Selection) []string {
	envType := strings.TrimSpace(selection.Type)
	if envType == "" {
		// Older frontends omit Type and expect remote-agent; the CLI default is local-agent.
		envType = "remote-agent"
	}
	args := []string{"init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment), "--type=" + envType}
	args = appendInitOptionalFlags(args, selection)
	// Only local-agent and host have a worktree on this machine.
	if envType == "local-agent" || envType == "host" {
		if localRepoPath := strings.TrimSpace(selection.LocalRepoPath); localRepoPath != "" {
			args = append(args, "--project-root", localRepoPath)
		}
	}
	args = append(
		args,
		"--set-default-tenant="+boolArg(selection.SetDefaultTenant),
		"--confirm-environment=true",
	)
	if selection.NoGit {
		args = append(args, "--no-git")
	}
	return args
}

func appendInitOptionalFlags(args []string, selection uimodel.Selection) []string {
	pairs := []struct{ flag, value string }{
		{"--version", strings.TrimSpace(selection.Version)},
		{"--runtime-image", strings.TrimSpace(selection.RuntimeImage)},
		{"--runtime-cpu", strings.TrimSpace(selection.RuntimeCPU)},
		{"--runtime-memory", strings.TrimSpace(selection.RuntimeMemory)},
		{"--kubernetes-context", strings.TrimSpace(selection.KubernetesContext)},
	}
	// The runtime chart registry is independent of the mutually exclusive image registry choices.
	pairs = append(pairs, struct{ flag, value string }{"--runtime-registry", strings.TrimSpace(selection.RuntimeRegistry)})
	// `erun init` rejects any pair of these.
	if !selection.ClusterRegistry && !selection.ErunRegistry {
		pairs = append(pairs, struct{ flag, value string }{"--container-registry", strings.TrimSpace(selection.ContainerRegistry)})
	}
	for _, pair := range pairs {
		if pair.value != "" {
			args = append(args, pair.flag, pair.value)
		}
	}
	for _, secret := range selection.ImagePullSecrets {
		if name := strings.TrimSpace(secret); name != "" {
			args = append(args, "--image-pull-secret", name)
		}
	}
	if selection.ClusterRegistry {
		args = append(args, "--cluster-registry")
	}
	if selection.ErunRegistry {
		args = append(args, "--erun-registry")
	}
	return args
}

func buildDeployArgs(selection uimodel.Selection) []string {
	args := []string{"deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		// No version means "redeploy what this env runs".
		args = append(args, "--current")
	}
	if image := DeployRuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	args = AppendDeployComponentsFlag(args, selection)
	return args
}

// An empty selection lets deploy fall back to the env's saved default.
func AppendDeployComponentsFlag(args []string, selection uimodel.Selection) []string {
	names := make([]string, 0, len(selection.Components))
	for _, raw := range selection.Components {
		if name := strings.TrimSpace(raw); name != "" {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return args
	}
	return append(args, "--components", strings.Join(names, ","))
}

// DeployRuntimeImageOverride returns "" when the pick is the env's own image.
func DeployRuntimeImageOverride(selection uimodel.Selection) string {
	image := strings.TrimSpace(selection.RuntimeImage)
	if image == "" {
		return ""
	}
	repo := image
	if idx := strings.LastIndex(repo, "/"); idx >= 0 {
		repo = repo[idx+1:]
	}
	if idx := strings.IndexAny(repo, ":@"); idx >= 0 {
		repo = repo[:idx]
	}
	if repo == config.RuntimeReleaseName(strings.TrimSpace(selection.Tenant)) {
		return ""
	}
	return image
}

func buildUpgradeArgs(selection uimodel.Selection) []string {
	args := []string{"upgrade", "--tenant", selection.Tenant, "--environment", selection.Environment}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	}
	return args
}

func buildCloudInitAWSArgs() []string {
	return []string{"cloud", "init", "aws"}
}

func buildCloudInitCloudflareArgs() []string {
	return []string{"cloud", "init", "cloudflare"}
}
