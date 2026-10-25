package helmrun

import (
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/helmdeploy"
	"github.com/sophium/erun/erun-common/infra/helm"
	"github.com/sophium/erun/erun-common/primitive/deploy/chartref"
	"github.com/sophium/erun/erun-common/primitive/deploy/helmargs"
)

func HelmDeployCommand(d helmdeploy.HelmDeploySpec) helm.CommandSpec {
	args := []string{
		"upgrade",
		"--install",
		"--wait",
		"--wait-for-jobs",
		// Pin server-side apply: helm's auto mode inherits a client-side release's
		// method, which rejects --force-conflicts. Forcing conflicts reclaims fields
		// an out-of-band kubectl edit took ownership of.
		"--server-side", "true",
		"--force-conflicts",
		"--timeout", d.Timeout,
		"--namespace", d.Namespace,
	}
	if d.Verbosity >= core.VerbosityDebug {
		args = append(args, "--debug")
	}
	if strings.TrimSpace(d.KubernetesContext) != "" {
		args = append(args, "--kube-context", d.KubernetesContext)
	}
	// Applied first so the config-dir overlay and re-scoped --sets win over it.
	if strings.TrimSpace(d.PulledValuesFilePath) != "" {
		args = append(args, "-f", d.PulledValuesFilePath)
	}
	if strings.TrimSpace(d.ValuesFilePath) != "" {
		args = append(args, "-f", d.ValuesFilePath)
	}
	args = append(args,
		"--set-string", "tenant="+d.Tenant,
		"--set-string", "environment="+d.Environment,
		"--set-string", "worktreeStorage="+d.WorktreeStorage,
		"--set-string", "worktreeRepoName="+d.WorktreeRepoName,
		"--set-string", "worktreeHostPath="+d.WorktreeHostPath,
		"--set", "sshdEnabled="+helmargs.FormatHelmBool(d.SSHDEnabled),
		"--set", "mcpPort="+helmargs.FormatHelmPort(d.MCPPort, configpkg.MCPServicePort),
		"--set", "apiPort="+helmargs.FormatHelmPort(d.APIPort, configpkg.APIServicePort),
		"--set", "sshPort="+helmargs.FormatHelmPort(d.SSHPort, configpkg.DefaultSSHLocalPort),
		"--set", "managedCloud="+helmargs.FormatHelmBool(d.ManagedCloud),
	)
	args = append(args, helmargs.HelmCloudContextSetArgs(d)...)
	args = append(args, helmargs.HelmOIDCAllowedIssuersSetArgs(d.OIDCAllowedIssuers)...)
	args = append(args,
		"--set", "api.postgres.reset="+helmargs.FormatHelmBool(d.ResetDatabase),
	)
	if strings.TrimSpace(d.RepoURL) != "" {
		args = append(args,
			"--set-string", "repoUrl="+d.RepoURL,
			"--set-string", "repoRef="+d.RepoRef,
		)
	}
	// The token is delivered as a Secret, never as a --set value.
	if d.CloudflareEnabled {
		args = append(args,
			"--set", "cloudContext.cloudflare.enabled=true",
			"--set-string", "cloudContext.cloudflare.accountId="+d.CloudflareAccountID,
			"--set-string", "cloudContext.cloudflare.secretName="+d.CloudflareSecretName,
		)
	}
	// The public key is delivered as a Secret (applyMCPAuthSecret).
	if d.MCPAuthEnabled {
		args = append(args,
			"--set", "mcpAuth.enabled=true",
			"--set-string", "mcpAuth.secretName="+d.MCPAuthSecretName,
			"--set-string", "mcpAuth.issuer="+helmargs.EscapeHelmSetValue(d.MCPAuthIssuer),
			"--set-string", "mcpAuth.audience="+helmargs.EscapeHelmSetValue(d.MCPAuthAudience),
		)
	}
	args = append(args, helmargs.HelmStoppedSetArgs(d.Stopped)...)
	args = append(args, helmargs.HelmRegistrySetArgs(d)...)
	// Always set so a flip in either direction reconciles.
	args = append(args, "--set", "disableBuildScript="+helmargs.FormatHelmBool(d.DisableBuildScript))
	args = append(args, helmargs.HelmPlatformAccountSetArgs(d.PlatformAccount)...)
	for _, key := range helmargs.SortedStringMapKeys(d.ImageOverrides) {
		args = append(args, "--set-string", "imageOverrides."+key+"="+d.ImageOverrides[key])
	}
	args = append(args, helmargs.HelmImagePullSecretSetArgs(d.ImagePullSecrets)...)
	args = append(args, helmargs.HelmRegistryCredentialSecretSetArgs(d.RegistryCredentialSecretName)...)
	args = append(args, helmargs.HelmPlatformAliasSecretSetArgs(d.PlatformAliasSecretName)...)
	args = append(args, helmargs.HelmPlatformSetArgs(d.Platform)...)
	args = append(args,
		"--set-string", "idle.timeout="+helmargs.HelmIdleTimeout(d.Idle),
		"--set-string", "idle.workingHours="+helmargs.HelmIdleWorkingHours(d.Idle),
		"--set-string", "idle.timezone="+helmargs.HelmIdleTimezone(d.Idle),
		"--set", "idle.trafficBytes="+helmargs.FormatHelmInt64(helmargs.HelmIdleTrafficBytes(d.Idle)),
		"--set-string", "runtime.resources.limits.cpu="+configpkg.NormalizeRuntimePodResources(d.RuntimePod).CPU,
		"--set-string", "runtime.resources.limits.memory="+configpkg.NormalizeRuntimePodResources(d.RuntimePod).Memory,
		"--set-string", "runtime.dind.resources.limits.cpu="+configpkg.NormalizeRuntimeDindPodResources(d.RuntimeDindPod).CPU,
		"--set-string", "runtime.dind.resources.limits.memory="+configpkg.NormalizeRuntimeDindPodResources(d.RuntimeDindPod).Memory,
	)
	args = append(args, helmargs.HelmClaudeSetArgs(d.Claude, d.OpenRouter)...)
	prefixHelmSetKeys(args, d.SubchartKey)
	args = append(args,
		d.ReleaseName,
		d.ChartPath,
	)
	// Local charts get their Chart.yaml stamped by prepareHelmChartForDeploy instead.
	dir := d.ChartPath
	if chartref.IsOCIChartReference(d.ChartPath) {
		args = append(args, "--version", chartref.ResolvedChartVersion(d))
		dir = ""
	}

	return helm.CommandSpec{
		Dir:  dir,
		Name: "helm",
		Args: args,
	}
}

// prefixHelmSetKeys nests every --set* key under a wrapped umbrella's subchart key.
func prefixHelmSetKeys(args []string, prefix string) {
	if strings.TrimSpace(prefix) == "" {
		return
	}
	for i := 0; i+1 < len(args); i++ {
		if !strings.HasPrefix(args[i], "--set") {
			continue
		}
		if !strings.Contains(args[i+1], "=") {
			continue
		}
		args[i+1] = prefix + "." + args[i+1]
	}
}

// HelmImmutableSelectorError reports a Deployment whose immutable spec.selector
// changed; RunHelmDeploy deletes the Deployment and retries once.
type HelmImmutableSelectorError struct {
	Deployment string
	Err        error
}

func (e *HelmImmutableSelectorError) Error() string {
	if e == nil {
		return ""
	}
	message := "deploy failed: Deployment " + e.Deployment + " has an immutable selector that changed"
	if e.Err != nil {
		message += ": " + e.Err.Error()
	}
	return message
}

func (e *HelmImmutableSelectorError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}
