package cloud

import (
	"fmt"
	"strings"
	"sync"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/activity"
)

func StopCloudContext(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (configpkg.CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace("cloud-context stop: " + strings.TrimSpace(params.Name))
	status, err := changeCloudContextPowerState(ctx, store, params, deps, "stop-instances", CloudContextStatusStopped)
	switch {
	case err == nil:
	case isAWSIncorrectInstanceStateError(err):
		// Already not running is the desired end state; still wait for "stopped".
		ctx.Trace("cloud-context stop: instance is already in a non-running state — waiting for stopped")
		status, err = resolveCloudContextStatusForName(store, params.Name)
		if err != nil {
			return configpkg.CloudContextStatus{}, err
		}
	default:
		return configpkg.CloudContextStatus{}, err
	}
	provider, err := ResolveCloudProvider(store, status.CloudProviderAlias)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	// Wait for "stopped" so a follow-up start does not hit IncorrectInstanceState.
	if _, err := deps.RunAWS(ctx, provider, status.Region, []string{"ec2", "wait", "instance-stopped", "--instance-ids", status.InstanceID}); err != nil {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q: stop was accepted but the instance was not observed stopped — it may still be transitioning; check its state in the AWS console and retry: %w", status.Name, classifyCloudContextPowerError("stop-instances", status.CloudContextConfig, err))
	}
	return status, nil
}

func resolveCloudContextStatusForName(store CloudContextStore, name string) (configpkg.CloudContextStatus, error) {
	config, ok, err := findCloudContext(store, name)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if !ok {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(name))
	}
	return configpkg.CloudContextStatus{
		CloudContextConfig: configpkg.NormalizeCloudContextConfig(config),
		Status:             CloudContextStatusStopped,
	}, nil
}

// isAWSIncorrectInstanceStateError matches stderr text, the most stable signal across AWS CLI versions.
func isAWSIncorrectInstanceStateError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "IncorrectInstanceState")
}

// awsExpiredCredentialsMarkers are AWS CLI stderr signatures of an authentication failure.
var awsExpiredCredentialsMarkers = []string{
	"Token has expired",
	"SSO session associated with this profile has expired",
	"ExpiredToken",
	"RequestExpired",
	"Unable to locate credentials",
	"InvalidClientTokenId",
	"AuthFailure",
}

func isAWSExpiredCredentialsError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	for _, marker := range awsExpiredCredentialsMarkers {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

// classifyCloudContextPowerError makes stop-protection and expired-credential failures
// actionable; other errors pass through unchanged because callers match the raw AWS text.
func classifyCloudContextPowerError(awsAction string, config configpkg.CloudContextConfig, err error) error {
	switch {
	case err == nil:
		return nil
	case awsAction == "stop-instances" && strings.Contains(err.Error(), "OperationNotPermitted"):
		return fmt.Errorf("cloud context %q cannot be stopped: stop protection (DisableApiStop) is enabled on instance %s — turn it off first (`erun context enable-api-stop %s`, or the stop-protection toggle in the desktop titlebar), then retry: %w", config.Name, config.InstanceID, config.Name, err)
	case isAWSExpiredCredentialsError(err):
		return fmt.Errorf("cloud context %q: the AWS session for alias %q is expired or unavailable — sign in again (`erun cloud login --alias %s`, or the cloud login in the desktop settings), then retry: %w", config.Name, config.CloudProviderAlias, config.CloudProviderAlias, err)
	}
	return err
}

func StartCloudContext(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) (configpkg.CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	ctx.Trace(fmt.Sprintf("cloud-context start: name=%s force=%v", strings.TrimSpace(params.Name), params.Force))
	if err := enforceCloudContextStartWorkingHoursGate(ctx, store, params, deps); err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if err := ensureCloudContextHostStopProfileAssociation(ctx, store, params, deps); err != nil {
		ctx.Trace("skipping cloud context host-stop profile association: " + err.Error())
	}
	status, err := changeCloudContextPowerState(ctx, store, params, deps, "start-instances", CloudContextStatusRunning)
	if err != nil {
		status, err = recoverCloudContextStartFromTransitionalState(ctx, store, params, deps, err)
		if err != nil {
			return configpkg.CloudContextStatus{}, err
		}
	}
	return finalizeStartedCloudContext(ctx, store, deps, status)
}

func finalizeStartedCloudContext(ctx core.Context, store CloudContextStore, deps CloudContextDependencies, status configpkg.CloudContextStatus) (configpkg.CloudContextStatus, error) {
	deps = normalizeCloudContextDependencies(deps)
	provider, err := ResolveCloudProvider(store, status.CloudProviderAlias)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, status.Region, []string{"ec2", "wait", "instance-running", "--instance-ids", status.InstanceID}); err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	publicIP, err := describeCloudContextPublicIP(ctx, deps, provider, status.Region, status.InstanceID)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	status.PublicIP = publicIP
	if err := configureCloudKubeContext(ctx, deps, status.CloudContextConfig); err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	status.UpdatedAt = deps.Now().UTC().Format(time.RFC3339)
	if ctx.DryRun {
		return status, nil
	}
	if err := saveCloudContextConfig(store, status.CloudContextConfig); err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	return status, nil
}

func enforceCloudContextStartWorkingHoursGate(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies) error {
	if params.Force {
		ctx.Trace("cloud-context start: force=true bypasses working-hours gate")
		return nil
	}
	lookup, ok := store.(CloudContextEnvLookup)
	if !ok {
		return nil
	}
	ctx.Trace("cloud-context start: checking working-hours gate")
	reason, err := cloudContextStartBlockedByWorkingHours(store, lookup, params.Name, deps.Now())
	if err != nil {
		return err
	}
	if reason != "" {
		ctx.Trace("cloud-context start: gated: " + reason)
		return fmt.Errorf("cloud context %q cannot start: %s; pass force=true to override", strings.TrimSpace(params.Name), reason)
	}
	ctx.Trace("cloud-context start: working-hours gate clear")
	return nil
}

// recoverCloudContextStartFromTransitionalState retries a start rejected because the
// instance is still stopping; start-instances only accepts stopped instances.
func recoverCloudContextStartFromTransitionalState(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies, startErr error) (configpkg.CloudContextStatus, error) {
	if !isAWSIncorrectInstanceStateError(startErr) {
		return configpkg.CloudContextStatus{}, startErr
	}
	ctx.Trace("cloud-context start: instance is in a transitional state — waiting for stopped before retrying start-instances")
	recovered, recoverErr := resolveCloudContextStatusForName(store, params.Name)
	if recoverErr != nil {
		return configpkg.CloudContextStatus{}, recoverErr
	}
	provider, perr := ResolveCloudProvider(store, recovered.CloudProviderAlias)
	if perr != nil {
		return configpkg.CloudContextStatus{}, perr
	}
	if _, werr := deps.RunAWS(ctx, provider, recovered.Region, []string{"ec2", "wait", "instance-stopped", "--instance-ids", recovered.InstanceID}); werr != nil {
		return configpkg.CloudContextStatus{}, werr
	}
	return changeCloudContextPowerState(ctx, store, params, deps, "start-instances", CloudContextStatusRunning)
}

func refreshSingleCloudContextStatus(ctx core.Context, store configpkg.CloudReadStore, deps CloudContextDependencies, seed configpkg.CloudContextStatus) (configpkg.CloudContextStatus, error) {
	statuses, err := RefreshCloudContextStatuses(ctx, store, deps)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	name := strings.TrimSpace(seed.Name)
	for _, status := range statuses {
		if strings.TrimSpace(status.Name) == name {
			return status, nil
		}
	}
	return seed, nil
}

// InClusterKubernetesContext is the kube-context a runtime pod uses when the chart injects none.
const InClusterKubernetesContext = "in-cluster"

// IsInClusterCloudContext reports whether a context names this process's own cluster, which is always running.
func IsInClusterCloudContext(config configpkg.CloudContextConfig) bool {
	return strings.TrimSpace(config.Name) == InClusterKubernetesContext ||
		strings.TrimSpace(config.KubernetesContext) == InClusterKubernetesContext
}

// ensureCloudContextRunningForPreflight powers on the matched context; in-cluster contexts need nothing.
func ensureCloudContextRunningForPreflight(ctx core.Context, store CloudContextStore, deps CloudContextDependencies, status configpkg.CloudContextStatus) error {
	if IsInClusterCloudContext(status.CloudContextConfig) {
		return nil
	}
	// Preflight runs at most once per context per CLI run, so a live describe is affordable.
	live, err := refreshSingleCloudContextStatus(ctx, store, deps, status)
	if err != nil {
		return err
	}
	if strings.TrimSpace(live.Status) == CloudContextStatusRunning {
		return nil
	}
	_, err = StartCloudContext(ctx, store, CloudContextParams{Name: status.Name}, deps)
	return err
}

func CloudContextPreflight(store CloudContextStore, deps CloudContextDependencies) core.KubernetesContextPreflightFunc {
	var mu sync.Mutex
	started := make(map[string]struct{})
	return func(ctx core.Context, kubernetesContext string) error {
		kubernetesContext = strings.TrimSpace(kubernetesContext)
		if kubernetesContext == "" || store == nil {
			return nil
		}

		mu.Lock()
		if _, ok := started[kubernetesContext]; ok {
			mu.Unlock()
			return nil
		}
		mu.Unlock()

		status, ok, err := configpkg.FindCloudContextForKubernetesContext(store, kubernetesContext)
		if err != nil || !ok {
			return err
		}
		if err := ensureCloudContextRunningForPreflight(ctx, store, deps, status); err != nil {
			return err
		}

		mu.Lock()
		started[kubernetesContext] = struct{}{}
		if name := strings.TrimSpace(status.Name); name != "" {
			started[name] = struct{}{}
		}
		mu.Unlock()
		return nil
	}
}

// cloudContextStartBlockedByWorkingHours returns a reason only when every attached
// environment is outside its working hours.
func cloudContextStartBlockedByWorkingHours(store configpkg.CloudReadStore, lookup CloudContextEnvLookup, contextName string, now time.Time) (string, error) {
	kubeContext, ok, err := resolveCloudContextWorkingHoursKubeContext(store, contextName)
	if err != nil || !ok {
		return "", err
	}

	tenants, err := lookup.ListTenantConfigs()
	if err != nil {
		return "", err
	}
	var blockedReason string
	hasAttachedEnv := false
	for _, tenant := range tenants {
		envs, err := lookup.ListEnvConfigs(tenant.Name)
		if err != nil {
			return "", err
		}
		permitsStart, tenantReason := evaluateTenantWorkingHoursGate(tenant, envs, kubeContext, now, &hasAttachedEnv)
		if permitsStart {
			return "", nil
		}
		if blockedReason == "" {
			blockedReason = tenantReason
		}
	}
	if !hasAttachedEnv {
		return "", nil
	}
	return blockedReason, nil
}

// resolveCloudContextWorkingHoursKubeContext returns ok=false when there is nothing to gate.
func resolveCloudContextWorkingHoursKubeContext(store configpkg.CloudReadStore, contextName string) (string, bool, error) {
	contextName = strings.TrimSpace(contextName)
	if contextName == "" {
		return "", false, nil
	}
	config, ok, err := findCloudContext(store, contextName)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return "", false, nil
	}
	kubeContext := strings.TrimSpace(config.KubernetesContext)
	if kubeContext == "" {
		kubeContext = strings.TrimSpace(config.Name)
	}
	if kubeContext == "" {
		return "", false, nil
	}
	return kubeContext, true, nil
}

func evaluateTenantWorkingHoursGate(tenant configpkg.TenantConfig, envs []configpkg.EnvConfig, kubeContext string, now time.Time, hasAttachedEnv *bool) (bool, string) {
	var blockedReason string
	for _, env := range envs {
		if strings.TrimSpace(env.KubernetesContext) != kubeContext {
			continue
		}
		*hasAttachedEnv = true
		policy, err := env.Idle.Resolve()
		if err != nil {
			continue
		}
		outside, _, err := activity.WorkingHoursStatus(policy.WorkingHours, policy.Timezone, now)
		if err != nil {
			continue
		}
		if !outside {
			return true, ""
		}
		if blockedReason == "" {
			blockedReason = fmt.Sprintf("outside working hours %s for environment %s/%s", policy.WorkingHours, tenant.Name, env.Name)
		}
	}
	return false, blockedReason
}

// changeCloudContextPowerState returns the new Status without persisting it.
func changeCloudContextPowerState(ctx core.Context, store CloudContextStore, params CloudContextParams, deps CloudContextDependencies, awsAction, status string) (configpkg.CloudContextStatus, error) {
	if store == nil {
		return configpkg.CloudContextStatus{}, fmt.Errorf("store is required")
	}
	deps = normalizeCloudContextDependencies(deps)
	config, ok, err := findCloudContext(store, params.Name)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if !ok {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q is not configured", strings.TrimSpace(params.Name))
	}
	if config.InstanceID == "" {
		return configpkg.CloudContextStatus{}, fmt.Errorf("cloud context %q has no instance ID", config.Name)
	}
	provider, err := ResolveCloudProvider(store, config.CloudProviderAlias)
	if err != nil {
		return configpkg.CloudContextStatus{}, err
	}
	if _, err := deps.RunAWS(ctx, provider, config.Region, []string{"ec2", awsAction, "--instance-ids", config.InstanceID}); err != nil {
		return configpkg.CloudContextStatus{}, classifyCloudContextPowerError(awsAction, config, err)
	}
	return configpkg.CloudContextStatus{CloudContextConfig: configpkg.NormalizeCloudContextConfig(config), Status: status}, nil
}
