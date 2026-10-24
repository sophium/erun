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
		// The chart is the source of truth for a deploy. Pin server-side apply on
		// explicitly: Helm's default (`--server-side=auto`) inherits the previous
		// release's method, so a release first created under client-side apply keeps
		// using it, and the --force-conflicts below is then rejected outright. With
		// server-side apply, an out-of-band `kubectl` edit takes ownership of the
		// fields it touched, and a later deploy that sets those same fields is
		// otherwise rejected as a conflict — even when the value is unchanged.
		// Force conflicts so the release always reclaims its own fields.
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
	// A by-reference umbrella's bundled values.<env>.yaml (pulled from the
	// published chart) forwards the tenant's authored subchart values; it is
	// applied first so an operator's config-dir overlay (ValuesFilePath) layered
	// next still wins, and the re-scoped --sets below win over both.
	if strings.TrimSpace(d.PulledValuesFilePath) != "" {
		args = append(args, "-f", d.PulledValuesFilePath)
	}
	// A published OCI chart has no local values overlay file; every other
	// chart resolves one (possibly empty) next to its templates.
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
	// Runtime source mount: only a runtime env that opted into MountSource
	// carries a repo URL, so these --sets are absent for every other deploy and
	// existing plans stay byte-for-byte unchanged. The pod clones repoUrl at
	// repoRef into the PVC worktree on first boot.
	if strings.TrimSpace(d.RepoURL) != "" {
		args = append(args,
			"--set-string", "repoUrl="+d.RepoURL,
			"--set-string", "repoRef="+d.RepoRef,
		)
	}
	// Cloudflare credential wiring is appended only when an env attached a
	// Cloudflare alias, so existing (AWS-only / no-cloud) deploy plans are
	// byte-for-byte unchanged. The token is never a --set value — it is
	// delivered out-of-band as a Secret (applyCloudflareCredentialsSecret).
	if d.CloudflareEnabled {
		args = append(args,
			"--set", "cloudContext.cloudflare.enabled=true",
			"--set-string", "cloudContext.cloudflare.accountId="+d.CloudflareAccountID,
			"--set-string", "cloudContext.cloudflare.secretName="+d.CloudflareSecretName,
		)
	}
	// MCP auth is appended only when a trusted key is injected (desktop deploy),
	// so existing deploy plans are byte-for-byte unchanged. The public key rides
	// out-of-band as a Secret (applyMCPAuthSecret); only its name + the issuer
	// and per-env audience are helm values.
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
	// disableBuildScript is always set — a boolean projection must be able to
	// reconcile a flip in either direction, so the chart always receives the
	// actual value.
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
	// When the chart is an umbrella wrapping a canonical erun-<base> chart, every
	// --set targets the wrapped subchart's value scope, so prefix each key with
	// the subchart key. No-op (empty prefix) for a chart installed directly.
	prefixHelmSetKeys(args, d.SubchartKey)
	args = append(args,
		d.ReleaseName,
		d.ChartPath,
	)
	// A published OCI chart is pinned by its own version — usually the deploy
	// version, since one version covers chart and image when both are stamped at
	// the same release, but separately when the chart and the runtime image are
	// versioned on different lines. Local charts get their Chart.yaml stamped by
	// prepareHelmChartForDeploy instead.
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

// prefixHelmSetKeys nests every helm --set/--set-string/--set-json key under
// prefix (a wrapped umbrella's subchart key), rewriting `key=value` to
// `prefix.key=value` in place. An empty prefix is a no-op, so a chart installed
// directly keeps byte-identical top-level --sets. Only --set* value args are
// touched; base flags, -f, the release name, chart path, and --version are not.
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

// Error renders the locked release, what it blocks, and the recovery that
// clears it. Helm's own message ("another operation (install/upgrade/rollback)
// is in progress") names neither the release nor the remedy, and the command
// that clears the lock is a different one from the deploy the operator ran, so
// an error that stops at helm's words leaves the deploy looking like it simply
// did not work. The erun-level recovery is named first because it is the one
// reachable from the surface the operator is already using, with the
// equivalent kubectl command beside it for a caller with no erun target.
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

// doctorTarget renders the recovery's positional arguments, or "" when this
// error was raised on a path with no erun target to name.
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

// HelmImmutableSelectorError is returned by DeployHelmChart when helm aborts an
// upgrade because a Deployment's spec.selector — an immutable field — differs
// from the installed object. RunHelmDeploy recovers by deleting the named
// Deployment (whose PVCs are separate objects and survive) and retrying the
// upgrade once, so the recreated Deployment carries the new selector.
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
