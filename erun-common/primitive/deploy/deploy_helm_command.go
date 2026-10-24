package deploy

import (
	"fmt"
	"io"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/helm"
)

func (d HelmDeploySpec) command() helm.CommandSpec {
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
		"--set", "sshdEnabled="+formatHelmBool(d.SSHDEnabled),
		"--set", "mcpPort="+formatHelmPort(d.MCPPort, configpkg.MCPServicePort),
		"--set", "apiPort="+formatHelmPort(d.APIPort, configpkg.APIServicePort),
		"--set", "sshPort="+formatHelmPort(d.SSHPort, configpkg.DefaultSSHLocalPort),
		"--set", "managedCloud="+formatHelmBool(d.ManagedCloud),
	)
	args = append(args, helmCloudContextSetArgs(d)...)
	args = append(args, helmOIDCAllowedIssuersSetArgs(d.OIDCAllowedIssuers)...)
	args = append(args,
		"--set", "api.postgres.reset="+formatHelmBool(d.ResetDatabase),
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
			"--set-string", "mcpAuth.issuer="+escapeHelmSetValue(d.MCPAuthIssuer),
			"--set-string", "mcpAuth.audience="+escapeHelmSetValue(d.MCPAuthAudience),
		)
	}
	args = append(args, helmStoppedSetArgs(d.Stopped)...)
	args = append(args, helmRegistrySetArgs(d)...)
	// Always set so a flip in either direction reconciles.
	args = append(args, "--set", "disableBuildScript="+formatHelmBool(d.DisableBuildScript))
	args = append(args, helmPlatformAccountSetArgs(d.PlatformAccount)...)
	for _, key := range sortedStringMapKeys(d.ImageOverrides) {
		args = append(args, "--set-string", "imageOverrides."+key+"="+d.ImageOverrides[key])
	}
	args = append(args, helmImagePullSecretSetArgs(d.ImagePullSecrets)...)
	args = append(args, helmRegistryCredentialSecretSetArgs(d.RegistryCredentialSecretName)...)
	args = append(args, helmPlatformAliasSecretSetArgs(d.PlatformAliasSecretName)...)
	args = append(args, helmPlatformSetArgs(d.Platform)...)
	args = append(args,
		"--set-string", "idle.timeout="+helmIdleTimeout(d.Idle),
		"--set-string", "idle.workingHours="+helmIdleWorkingHours(d.Idle),
		"--set-string", "idle.timezone="+helmIdleTimezone(d.Idle),
		"--set", "idle.trafficBytes="+formatHelmInt64(helmIdleTrafficBytes(d.Idle)),
		"--set-string", "runtime.resources.limits.cpu="+configpkg.NormalizeRuntimePodResources(d.RuntimePod).CPU,
		"--set-string", "runtime.resources.limits.memory="+configpkg.NormalizeRuntimePodResources(d.RuntimePod).Memory,
		"--set-string", "runtime.dind.resources.limits.cpu="+configpkg.NormalizeRuntimeDindPodResources(d.RuntimeDindPod).CPU,
		"--set-string", "runtime.dind.resources.limits.memory="+configpkg.NormalizeRuntimeDindPodResources(d.RuntimeDindPod).Memory,
	)
	args = append(args, helmClaudeSetArgs(d.Claude, d.OpenRouter)...)
	prefixHelmSetKeys(args, d.SubchartKey)
	args = append(args,
		d.ReleaseName,
		d.ChartPath,
	)
	// Local charts get their Chart.yaml stamped by prepareHelmChartForDeploy instead.
	dir := d.ChartPath
	if isOCIChartReference(d.ChartPath) {
		args = append(args, "--version", d.resolvedChartVersion())
		dir = ""
	}

	return helm.CommandSpec{
		Dir:  dir,
		Name: "helm",
		Args: args,
	}
}

func isOCIChartReference(chartPath string) bool {
	return strings.HasPrefix(strings.TrimSpace(chartPath), "oci://")
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

func (p HelmReleaseRecoveryParams) Command() helm.CommandSpec {
	args := []string{}
	if p.Verbosity >= core.VerbosityDebug {
		args = append(args, "--v=4")
	}
	if strings.TrimSpace(p.KubernetesContext) != "" {
		args = append(args, "--context", p.KubernetesContext)
	}
	args = append(args,
		"--namespace", p.Namespace,
		"delete",
		"secrets,configmaps",
		"-l", helmPendingReleaseOperationSelector(p.ReleaseName),
		"--ignore-not-found",
	)

	return helm.CommandSpec{
		Name: "kubectl",
		Args: args,
	}
}

func helmPendingReleaseOperationSelector(releaseName string) string {
	return "owner=helm,name=" + releaseName + ",status in (pending-install,pending-upgrade,pending-rollback)"
}

// Error names the locked release and its recovery; helm's own message names
// neither.
func (e *HelmReleasePendingOperationError) Error() string {
	if e == nil {
		return ""
	}
	message := strings.TrimSpace(e.Message)
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if message == "" {
		message = "another operation (install/upgrade/rollback) is in progress"
	}
	release := strings.TrimSpace(e.ReleaseName)
	if release == "" {
		release = "the release"
	}
	diagnosis := fmt.Sprintf("helm release %q is locked by an unfinished install/upgrade/rollback, so this deploy cannot proceed", release)
	if namespace := strings.TrimSpace(e.Namespace); namespace != "" {
		diagnosis += " in namespace " + namespace
	}
	if target := e.doctorTarget(); target != "" {
		return fmt.Sprintf("%s; clear the lock with `erun doctor --clear-pending-helm %s`, or with `%s`: %s",
			diagnosis, target, e.RecoveryCommand(), message)
	}
	return fmt.Sprintf("%s; clear the lock with `%s`: %s", diagnosis, e.RecoveryCommand(), message)
}

func (e *HelmReleasePendingOperationError) doctorTarget() string {
	tenant := strings.TrimSpace(e.Tenant)
	environment := strings.TrimSpace(e.Environment)
	if tenant == "" || environment == "" {
		return ""
	}
	return tenant + " " + environment
}

func (e *HelmReleasePendingOperationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
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

func (e *HelmReleasePendingOperationError) RecoveryParams(verbosity int, stdout, stderr io.Writer) HelmReleaseRecoveryParams {
	if e == nil {
		return HelmReleaseRecoveryParams{Verbosity: verbosity, Stdout: stdout, Stderr: stderr}
	}
	return HelmReleaseRecoveryParams{
		ReleaseName:       e.ReleaseName,
		Namespace:         e.Namespace,
		KubernetesContext: e.KubernetesContext,
		Verbosity:         verbosity,
		Stdout:            stdout,
		Stderr:            stderr,
	}
}

func (e *HelmReleasePendingOperationError) RecoveryCommand() string {
	if e == nil {
		return ""
	}
	command := e.RecoveryParams(0, nil, nil).Command()
	return core.FormatShellCommand(command.Dir, command.Name, command.Args...)
}
