package stop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/config/execmode"
	"github.com/sophium/erun/erun-common/base/config/runtimetarget"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/remotesession"
	"github.com/sophium/erun/erun-common/infra/cloud/runtimewait"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/deploy/components"
)

// A stopped environment's runtime Deployment is scaled to zero; PVCs survive. The intent is
// recorded in env config so helm upgrades keep replicas at 0, and only an operator open, never a
// session reconnect, wakes it, or reattach would undo the stop that dropped the session.

// RuntimeScaleTarget identifies one environment's runtime Deployment.
type RuntimeScaleTarget struct {
	Tenant            string
	Environment       string
	Namespace         string
	ReleaseName       string
	KubernetesContext string
}

// RuntimeScaleTargetForResult derives the scale target from a resolved open target.
func RuntimeScaleTargetForResult(result config.OpenResult) RuntimeScaleTarget {
	return RuntimeScaleTarget{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		Namespace:         runtimetarget.KubernetesNamespaceName(result.Tenant, result.Environment),
		ReleaseName:       runtimetarget.RuntimeReleaseName(result.Tenant),
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
	}
}

// RuntimeRunState is the cluster's view of the runtime Deployment; Present is false when undeployed.
type RuntimeRunState struct {
	Present bool
	// DesiredReplicas defaults to 1 on an unreadable answer so it is never mistaken for a stop.
	DesiredReplicas int
	ReadyReplicas   int
}

// Stopped reports a present Deployment asking for zero pods, distinct from an unhealthy one.
func (s RuntimeRunState) Stopped() bool {
	return s.Present && s.DesiredReplicas == 0
}

// RuntimeStopDecision is what a stop would do.
type RuntimeStopDecision struct {
	Scale          bool
	PersistIntent  bool
	AlreadyStopped bool
}

// DecideRuntimeStop still persists intent for an already-scaled-down environment, or the next
// helm upgrade would bring it back.
func DecideRuntimeStop(state RuntimeRunState, stoppedIntent bool) RuntimeStopDecision {
	return RuntimeStopDecision{
		Scale:          !state.Stopped(),
		PersistIntent:  !stoppedIntent,
		AlreadyStopped: state.Stopped(),
	}
}

// RuntimeWakeDecision is what a wake would do. RefuseStopped tells a reconnect not to proceed.
type RuntimeWakeDecision struct {
	Scale          bool
	ClearIntent    bool
	AlreadyRunning bool
	RefuseStopped  bool
}

// DecideRuntimeWake resolves a wake. A reconnect never wakes a stopped environment or clears its
// intent, since the stop itself dropped the session.
func DecideRuntimeWake(state RuntimeRunState, stoppedIntent, reconnect bool) RuntimeWakeDecision {
	if reconnect {
		return RuntimeWakeDecision{
			AlreadyRunning: state.Present && !state.Stopped(),
			RefuseStopped:  state.Stopped(),
		}
	}
	return RuntimeWakeDecision{
		Scale:          state.Stopped(),
		ClearIntent:    stoppedIntent,
		AlreadyRunning: state.Present && !state.Stopped(),
	}
}

// StopEnvironmentParams is a stop request.
type StopEnvironmentParams struct {
	Result        config.OpenResult
	SaveEnvConfig func(string, config.EnvConfig) error
}

// StopRemainingComponents names the env's deployed components other than the runtime, which a
// stop leaves running since on a platform-hosting env they are the platform. Read from config to
// avoid a second cluster query.
func StopRemainingComponents(result config.OpenResult) []string {
	names := components.SelectedPublishableComponents(result.EnvConfig.Deploy.Components, result.Tenant, config.ProjectK8sConfig{})
	// Nil, not empty, so struct comparisons in tests stay meaningful.
	if len(names) == 0 {
		return nil
	}
	return names
}

// RunStopEnvironment scales the runtime to zero and, only once the cluster confirms, records the stop intent.
func RunStopEnvironment(ctx core.Context, params StopEnvironmentParams) (model.StopEnvironmentResult, error) {
	target := RuntimeScaleTargetForResult(params.Result)
	result := model.StopEnvironmentResult{
		Tenant:            target.Tenant,
		Environment:       target.Environment,
		Namespace:         target.Namespace,
		Release:           target.ReleaseName,
		KubernetesContext: target.KubernetesContext,
	}

	state, err := ReadRuntimeRunState(ctx, target)
	if err != nil {
		return model.StopEnvironmentResult{}, err
	}
	if !state.Present {
		return model.StopEnvironmentResult{}, fmt.Errorf("runtime for %s/%s is not deployed (deployment %q not found in namespace %q); nothing to stop",
			target.Tenant, target.Environment, target.ReleaseName, target.Namespace)
	}

	decision := DecideRuntimeStop(state, params.Result.EnvConfig.Stopped)
	result.Stopped = true
	result.AlreadyStopped = decision.AlreadyStopped
	result.RemainingComponents = StopRemainingComponents(params.Result)
	// They explain why a stopped environment still shows running pods.
	if len(result.RemainingComponents) > 0 {
		ctx.Trace(fmt.Sprintf("stop: platform component(s) in namespace %s are not touched and keep running: %s",
			target.Namespace, strings.Join(result.RemainingComponents, ", ")))
	}
	if decision.AlreadyStopped {
		ctx.Trace(fmt.Sprintf("stop: %s/%s is already stopped (deployment %s wants 0 replicas)", target.Tenant, target.Environment, target.ReleaseName))
	} else {
		result.EndedSessions = traceAttachedAppSessions(ctx, target)
		ctx.Trace(fmt.Sprintf("stop: scaling %s to 0 replicas to return its runtime and dind capacity to the node", target.ReleaseName))
		if err := scaleRuntimeDeployment(ctx, target, 0); err != nil {
			return model.StopEnvironmentResult{}, err
		}
		if err := confirmRuntimeStopped(ctx, target); err != nil {
			return model.StopEnvironmentResult{}, err
		}
	}
	if decision.PersistIntent {
		if err := persistEnvironmentStopIntent(ctx, params.Result, params.SaveEnvConfig, true); err != nil {
			return model.StopEnvironmentResult{}, err
		}
	}
	ctx.Trace(fmt.Sprintf("==> Stopped %s/%s", target.Tenant, target.Environment))
	return result, nil
}

// confirmRuntimeStopped fails a stop whose replica count bounced back.
func confirmRuntimeStopped(ctx core.Context, target RuntimeScaleTarget) error {
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("stop: would re-read %s to confirm it wants 0 replicas before reporting success", target.ReleaseName))
		return nil
	}
	state, err := ReadRuntimeRunState(ctx, target)
	if err != nil {
		return fmt.Errorf("confirm %s/%s stopped: %w", target.Tenant, target.Environment, err)
	}
	if !state.Stopped() {
		return fmt.Errorf("stop of %s/%s did not take effect: deployment %q in namespace %q still wants %d replica(s) after the scale",
			target.Tenant, target.Environment, target.ReleaseName, target.Namespace, state.DesiredReplicas)
	}
	return nil
}

func traceAttachedAppSessions(ctx core.Context, target RuntimeScaleTarget) []string {
	sessions, err := readAttachedAppSessions(ctx, target)
	if err != nil {
		ctx.Trace(fmt.Sprintf("stop: could not list the desktop sessions attached to %s (%s); stopping anyway", target.ReleaseName, strings.TrimSpace(err.Error())))
		return nil
	}
	if len(sessions) == 0 {
		ctx.Trace(fmt.Sprintf("stop: no desktop session is attached to %s", target.ReleaseName))
		return nil
	}
	ids := make([]string, 0, len(sessions))
	labels := make([]string, 0, len(sessions))
	for _, session := range sessions {
		ids = append(ids, session.ID)
		if program := strings.TrimSpace(session.Program); program != "" {
			labels = append(labels, session.ID+" ("+program+")")
			continue
		}
		labels = append(labels, session.ID)
	}
	ctx.Trace(fmt.Sprintf("stop: %d attached desktop session(s) end with the pod: %s", len(labels), strings.Join(labels, ", ")))
	return ids
}

// Read-only, so it also runs under --dry-run.
func readAttachedAppSessions(ctx core.Context, target RuntimeScaleTarget) ([]remotesession.RemoteAppSessionHeartbeat, error) {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "exec", "deployment/"+target.ReleaseName, "--", "/bin/sh", "-lc",
		remotesession.RemoteAppSessionHeartbeatScript(target.Tenant, target.Environment))

	ctx.TraceCommand("", "kubectl", args...)
	output, err := core.Command("kubectl", args...).Output()
	if err != nil {
		if stderr := core.StderrFromExitError(err); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
		return nil, err
	}
	return remotesession.ParseRemoteAppSessionHeartbeats(string(output)), nil
}

// ClearEnvironmentStopIntent clears the recorded stop without touching the cluster; `open --deploy`
// calls it before helm renders so the rollout brings the pod up.
func ClearEnvironmentStopIntent(ctx core.Context, result config.OpenResult, saveEnvConfig func(string, config.EnvConfig) error) (config.EnvConfig, error) {
	if !result.EnvConfig.Stopped {
		return result.EnvConfig, nil
	}
	if err := persistEnvironmentStopIntent(ctx, result, saveEnvConfig, false); err != nil {
		return config.EnvConfig{}, err
	}
	updated := result.EnvConfig
	updated.Stopped = false
	return updated, nil
}

// WakeEnvironmentParams is a wake request; Reconnect marks a supervisor reattaching a session.
type WakeEnvironmentParams struct {
	Result        config.OpenResult
	SaveEnvConfig func(string, config.EnvConfig) error
	Reconnect     bool
}

// WakeEnvironmentResult reports what the wake did.
type WakeEnvironmentResult struct {
	EnvConfig      config.EnvConfig
	Woken          bool
	AlreadyRunning bool
}

// EnsureEnvironmentAwake starts a stopped environment, since port-forward cannot attach to zero
// replicas. A reconnecting caller gets an error instead.
func EnsureEnvironmentAwake(ctx core.Context, params WakeEnvironmentParams) (WakeEnvironmentResult, error) {
	target := RuntimeScaleTargetForResult(params.Result)
	result := WakeEnvironmentResult{EnvConfig: params.Result.EnvConfig}

	// An unreadable state must not abort the open: presume running, which never scales anything away.
	state, err := ReadRuntimeRunState(ctx, target)
	if err != nil {
		ctx.Trace("open: could not read the runtime's replica count (" + err.Error() + "); assuming it is running and continuing")
		state = RuntimeRunState{Present: true, DesiredReplicas: 1}
	}
	decision := DecideRuntimeWake(state, params.Result.EnvConfig.Stopped, params.Reconnect)
	result.AlreadyRunning = decision.AlreadyRunning
	if decision.RefuseStopped {
		return WakeEnvironmentResult{}, fmt.Errorf(
			"%s/%s is stopped, and a session reconnect does not start it; run `erun open %s %s` to start it again",
			target.Tenant, target.Environment, target.Tenant, target.Environment)
	}
	if decision.ClearIntent {
		updated, err := ClearEnvironmentStopIntent(ctx, params.Result, params.SaveEnvConfig)
		if err != nil {
			return WakeEnvironmentResult{}, err
		}
		result.EnvConfig = updated
	}
	if !state.Present {
		ctx.Trace(fmt.Sprintf("open: %s is not deployed, so there is nothing to wake", target.ReleaseName))
		return result, nil
	}
	if !decision.Scale {
		return result, nil
	}

	ctx.Info(fmt.Sprintf("==> Waking stopped environment %s/%s", target.Tenant, target.Environment))
	if err := scaleRuntimeDeployment(ctx, target, 1); err != nil {
		return WakeEnvironmentResult{}, err
	}
	if err := WaitRuntimeAvailable(ctx, target); err != nil {
		return WakeEnvironmentResult{}, err
	}
	result.Woken = true
	ctx.Info(fmt.Sprintf("==> Woke %s/%s", target.Tenant, target.Environment))
	return result, nil
}

// Deploy renders the chart's `stopped` value from this, so it survives helm reconciliation.
func persistEnvironmentStopIntent(ctx core.Context, result config.OpenResult, saveEnvConfig func(string, config.EnvConfig) error, stopped bool) error {
	updated := result.EnvConfig
	updated.Stopped = stopped
	if ctx.DryRun {
		ctx.Trace(fmt.Sprintf("config: would assign stopped=%t to %s/%s", stopped, result.Tenant, result.Environment))
		return nil
	}
	if saveEnvConfig == nil {
		return fmt.Errorf("persist stop intent: env config storage is not wired")
	}
	if err := saveEnvConfig(result.Tenant, updated); err != nil {
		return fmt.Errorf("persist stop intent: %w", err)
	}
	ctx.Trace(fmt.Sprintf("config: assigned stopped=%t to %s/%s", stopped, result.Tenant, result.Environment))
	return nil
}

// Both fields are read in one call so they cannot be observed a moment apart.
const runtimeRunStateSeparator = "/"

// ReadRuntimeRunState is read-only, so it runs under --dry-run too.
func ReadRuntimeRunState(ctx core.Context, target RuntimeScaleTarget) (RuntimeRunState, error) {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "get", "deployment", target.ReleaseName, "-o",
		"jsonpath={.spec.replicas}"+runtimeRunStateSeparator+"{.status.readyReplicas}")

	ctx.TraceCommand("", "kubectl", args...)
	rawOutput, err := core.Command("kubectl", args...).CombinedOutput()
	output := string(rawOutput)
	if err != nil {
		if isKubernetesNotFoundMessage(output) {
			ctx.Trace(fmt.Sprintf("runtime run state: deployment %s is absent from namespace %s", target.ReleaseName, target.Namespace))
			return RuntimeRunState{}, nil
		}
		return RuntimeRunState{}, fmt.Errorf("failed to read deployment %q: %w", target.ReleaseName, err)
	}
	state := parseRuntimeRunState(output)
	ctx.Trace(fmt.Sprintf("runtime run state: deployment %s wants %d replica(s), %d ready", target.ReleaseName, state.DesiredReplicas, state.ReadyReplicas))
	return state, nil
}

// An empty ready field means unset, which is zero.
func parseRuntimeRunState(output string) RuntimeRunState {
	desiredRaw, readyRaw, _ := strings.Cut(strings.TrimSpace(output), runtimeRunStateSeparator)
	state := RuntimeRunState{Present: true, DesiredReplicas: 1}
	if desired, err := strconv.Atoi(strings.TrimSpace(desiredRaw)); err == nil {
		state.DesiredReplicas = desired
	}
	if ready, err := strconv.Atoi(strings.TrimSpace(readyRaw)); err == nil {
		state.ReadyReplicas = ready
	}
	return state
}

func isKubernetesNotFoundMessage(output string) bool {
	message := strings.ToLower(output)
	return strings.Contains(message, "notfound") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "no resources found")
}

func scaleRuntimeDeployment(ctx core.Context, target RuntimeScaleTarget, replicas int) error {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "scale", "deployment/"+target.ReleaseName, "--replicas="+strconv.Itoa(replicas))
	return core.RunRawCommand(ctx, model.RawCommandSpec{Args: append([]string{"kubectl"}, args...)}, nil)
}

// WaitRuntimeAvailable blocks until the runtime Deployment reports Available.
func WaitRuntimeAvailable(ctx core.Context, target RuntimeScaleTarget) error {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "wait", "--for=condition=Available", "--timeout", runtimewait.DefaultShellLaunchWaitTimeout, "deployment/"+target.ReleaseName)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if execmode.CurrentExecutionMode(kube.KubectlDeploymentWaitExecutionOperation) == execmode.ExecutionModeLibrary {
		return kube.LibraryWaitForDeploymentAvailable(target.KubernetesContext, target.Namespace, target.ReleaseName, runtimewait.DefaultShellLaunchWaitTimeout)
	}
	return core.RawCommandRunner("", "kubectl", args, ctx.Stdin, ctx.Stdout, ctx.Stderr)
}

func kubectlRuntimeTargetArgs(target RuntimeScaleTarget) []string {
	args := make([]string, 0, 4)
	if strings.TrimSpace(target.KubernetesContext) != "" {
		args = append(args, "--context", target.KubernetesContext)
	}
	if strings.TrimSpace(target.Namespace) != "" {
		args = append(args, "--namespace", target.Namespace)
	}
	return args
}
