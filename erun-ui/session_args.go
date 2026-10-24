package main

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

func buildSSHDInitArgs(selection uiSelection) []string {
	return []string{"sshd", "init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

func buildDoctorArgs(selection uiSelection) []string {
	return []string{"doctor", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
}

// buildOpenNoShellArgs builds the forwarder-rebind open. Every caller is the
// desktop reacting to a dropped connection rather than the operator asking for
// the environment, so it carries --reconnect: a rebind must reattach to a
// running environment, never start the one the operator just stopped.
func buildOpenNoShellArgs(tenant, environment string) []string {
	return []string{"open", strings.TrimSpace(tenant), strings.TrimSpace(environment), "--no-shell", "--no-alias-prompt", "--reconnect"}
}

func buildInitArgs(selection uiSelection) []string {
	envType := strings.TrimSpace(selection.Type)
	if envType == "" {
		// Older frontends that don't yet send Type still expect the
		// pre-Type behavior — pinned to remote-agent. The CLI itself
		// defaults to local-agent when --type is unset, which would be
		// a silent behavior change for the desktop flow.
		envType = "remote-agent"
	}
	args := []string{"init", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment), "--type=" + envType}
	args = appendInitOptionalFlags(args, selection)
	// local-agent and host both resolve their worktree from a directory on this
	// machine, so both pass it through as --project-root; init refuses a host
	// create outright when it has no directory to use. remote-agent checks out
	// its own worktree on a cluster PVC and runtime has no worktree at all, so
	// neither takes the flag.
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

func appendInitOptionalFlags(args []string, selection uiSelection) []string {
	pairs := []struct{ flag, value string }{
		{"--version", strings.TrimSpace(selection.Version)},
		{"--runtime-image", strings.TrimSpace(selection.RuntimeImage)},
		{"--runtime-cpu", strings.TrimSpace(selection.RuntimeCPU)},
		{"--runtime-memory", strings.TrimSpace(selection.RuntimeMemory)},
		{"--kubernetes-context", strings.TrimSpace(selection.KubernetesContext)},
	}
	// Where erun's own chart is pulled from is a separate coordinate from where
	// the project's images go, so it is not part of the mutually exclusive set
	// below.
	pairs = append(pairs, struct{ flag, value string }{"--runtime-registry", strings.TrimSpace(selection.RuntimeRegistry)})
	// The three registry choices are mutually exclusive (`erun init` rejects any
	// pair); the explicit hosted and cluster selections win over a static string.
	if !selection.ClusterRegistry && !selection.ErunRegistry {
		pairs = append(pairs, struct{ flag, value string }{"--container-registry", strings.TrimSpace(selection.ContainerRegistry)})
	}
	for _, pair := range pairs {
		if pair.value != "" {
			args = append(args, pair.flag, pair.value)
		}
	}
	// Repeated rather than comma-joined: a secret name may legally contain no
	// comma, but repeating is what the flag documents and never needs escaping.
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

func buildDeployArgs(selection uiSelection) []string {
	args := []string{"deploy", strings.TrimSpace(selection.Tenant), strings.TrimSpace(selection.Environment)}
	if version := strings.TrimSpace(selection.Version); version != "" {
		args = append(args, "--version", version)
	} else {
		// deploy is a pure primitive: it requires an explicit version. With no
		// version the operator means "redeploy what this env already runs", so
		// install the persisted version via --current rather than erroring.
		args = append(args, "--current")
	}
	if image := deployRuntimeImageOverride(selection); image != "" {
		args = append(args, "--runtime-image", image)
	}
	args = appendDeployComponentsFlag(args, selection)
	return args
}

// appendDeployComponentsFlag threads the operator's exact component selection
// into the pure deploy primitive rather than reaching for a convenience switch
// (erun-ui/AGENTS.md); an empty selection appends nothing so deploy falls back to
// the env's saved default.
func appendDeployComponentsFlag(args []string, selection uiSelection) []string {
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

// deployRuntimeImageOverride returns the runtime image to force via
// `deploy --runtime-image`, or "" when the pick is the env's own image and needs
// no override. An override lets an operator bootstrap a new env on the shared
// ERun base image before the tenant's own <tenant>-devops image exists.
func deployRuntimeImageOverride(selection uiSelection) string {
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

// buildUpgradeArgs scopes `erun upgrade` to one env so the Upgrade-all flow can
// upgrade each member in parallel; a selection Version pins the exact target the
// operator picked from the versions an env's registries offered.
func buildUpgradeArgs(selection uiSelection) []string {
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
