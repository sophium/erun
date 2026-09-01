package eruncommon

import (
	"fmt"
	"strconv"
	"strings"
)

// A stopped environment is one whose runtime Deployment is scaled to zero. That
// returns both the runtime container's limits and its unlimited dind sidecar's
// real consumption to the node, so the environments an operator is actually
// using can be offered the capacity. Every PVC survives — the home tree, the
// docker/buildkit state, and a local-agent env's hostPath worktree — so waking
// is a pod start, not a cold rebuild.
//
// The intent is durable in two places that must agree: the env config's
// Stopped flag (what the operator asked for) and the chart's `stopped` value
// deploy renders from it (so a helm upgrade reconciles replicas instead of
// silently undoing the scale patch). The cluster stays the source of truth for
// what is displayed; the config only records intent.
//
// Starting an environment again is an operator gesture — opening it — and that
// is the whole reason the wake below takes a reconnect flag. A stop drops every
// session attached to the pod, and whatever supervises those sessions will try
// to re-establish them; if that reattach counted as an open, it would scale the
// runtime back up and erase the recorded intent within a second of the stop,
// for as long as anything stayed attached. So the wake asks who is calling.

// RuntimeScaleTarget identifies one environment's runtime Deployment.
type RuntimeScaleTarget struct {
	Tenant            string
	Environment       string
	Namespace         string
	ReleaseName       string
	KubernetesContext string
}

// RuntimeScaleTargetForResult derives the scale target from a resolved open
// target, so stop, wake, and the shell launcher all address the same Deployment.
func RuntimeScaleTargetForResult(result OpenResult) RuntimeScaleTarget {
	return RuntimeScaleTarget{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		Namespace:         KubernetesNamespaceName(result.Tenant, result.Environment),
		ReleaseName:       RuntimeReleaseName(result.Tenant),
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
	}
}

// RuntimeRunState is what the cluster reports about an environment's runtime
// Deployment. Absent is the undeployed case, distinct from a deployed
// Deployment asking for zero replicas.
type RuntimeRunState struct {
	Present bool
	// DesiredReplicas is the Deployment's spec.replicas. When kubectl answers
	// with something that is not a replica count it stays at the API server's own
	// default of 1: an unreadable answer must never be mistaken for a deliberate
	// stop, which would scale a healthy environment away.
	DesiredReplicas int
	ReadyReplicas   int
}

// Stopped reports the scaled-to-zero state: the Deployment exists and asks for
// no pods. Deliberately distinct from "pods exist but are not ready", which is
// an unhealthy environment, not a stopped one.
func (s RuntimeRunState) Stopped() bool {
	return s.Present && s.DesiredReplicas == 0
}

// RuntimeStopDecision is what a stop would do, resolved from the cluster state
// and the recorded intent before anything is executed.
type RuntimeStopDecision struct {
	Scale          bool
	PersistIntent  bool
	AlreadyStopped bool
}

// DecideRuntimeStop resolves a stop against the cluster state and the intent the
// env config already records. An environment that is already scaled to zero
// needs no scale patch, but still needs the intent persisted when the config
// does not carry it yet — otherwise the next helm upgrade brings it back.
func DecideRuntimeStop(state RuntimeRunState, stoppedIntent bool) RuntimeStopDecision {
	return RuntimeStopDecision{
		Scale:          !state.Stopped(),
		PersistIntent:  !stoppedIntent,
		AlreadyStopped: state.Stopped(),
	}
}

// RuntimeWakeDecision is what a wake would do. ClearIntent is separate from
// Scale because `open --deploy` clears the intent before helm renders the
// chart, and lets the rollout itself bring the pod up. RefuseStopped is the
// reconnect answer: nothing to do, and the caller must not proceed as if the
// environment were up.
type RuntimeWakeDecision struct {
	Scale          bool
	ClearIntent    bool
	AlreadyRunning bool
	RefuseStopped  bool
}

// DecideRuntimeWake resolves a wake. An environment that is already running and
// carries no stop intent needs neither a scale call nor a config write, which is
// what keeps `erun open` quiet on the common path.
//
// reconnect marks the caller as a supervisor re-establishing a dropped session
// rather than an operator asking for the environment. Only the operator's
// gesture starts a stopped environment or retires the recorded stop: a stop is
// precisely what drops every attached session, so a reconnect that woke would
// undo the stop that triggered it, on a loop, and erase the intent meant to
// outlive it.
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

// StopEnvironmentParams is a stop request: the resolved target plus the writer
// that records the operator's intent on the env config.
type StopEnvironmentParams struct {
	Result        OpenResult
	SaveEnvConfig func(string, EnvConfig) error
}

// StopEnvironmentResult is the transport-neutral outcome of `erun stop`.
type StopEnvironmentResult struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	Namespace         string `json:"namespace"`
	Release           string `json:"release"`
	KubernetesContext string `json:"kubernetesContext"`
	// Stopped is the state after this run; AlreadyStopped distinguishes the
	// no-op from the run that actually reclaimed the node's capacity.
	Stopped        bool `json:"stopped"`
	AlreadyStopped bool `json:"alreadyStopped"`
	// EndedSessions names the desktop terminal sessions the stop took down with
	// the pod, so the outcome for attached tabs is stated rather than left for
	// the operator to infer from tabs going dark.
	EndedSessions []string `json:"endedSessions,omitempty"`
}

// RunStopEnvironment scales the environment's runtime Deployment to zero and
// records the intent so a later helm upgrade re-renders replicas: 0 instead of
// quietly restarting the pod. The intent is written last and only once the
// cluster has confirmed the scale, so the config never records a stop that did
// not happen.
func RunStopEnvironment(ctx Context, params StopEnvironmentParams) (StopEnvironmentResult, error) {
	target := RuntimeScaleTargetForResult(params.Result)
	result := StopEnvironmentResult{
		Tenant:            target.Tenant,
		Environment:       target.Environment,
		Namespace:         target.Namespace,
		Release:           target.ReleaseName,
		KubernetesContext: target.KubernetesContext,
	}

	state, err := ReadRuntimeRunState(ctx, target)
	if err != nil {
		return StopEnvironmentResult{}, err
	}
	if !state.Present {
		return StopEnvironmentResult{}, fmt.Errorf("runtime for %s/%s is not deployed (deployment %q not found in namespace %q); nothing to stop",
			target.Tenant, target.Environment, target.ReleaseName, target.Namespace)
	}

	decision := DecideRuntimeStop(state, params.Result.EnvConfig.Stopped)
	result.Stopped = true
	result.AlreadyStopped = decision.AlreadyStopped
	if decision.AlreadyStopped {
		ctx.Trace(fmt.Sprintf("stop: %s/%s is already stopped (deployment %s wants 0 replicas)", target.Tenant, target.Environment, target.ReleaseName))
	} else {
		result.EndedSessions = traceAttachedAppSessions(ctx, target)
		ctx.Trace(fmt.Sprintf("stop: scaling %s to 0 replicas to return its runtime and dind capacity to the node", target.ReleaseName))
		if err := scaleRuntimeDeployment(ctx, target, 0); err != nil {
			return StopEnvironmentResult{}, err
		}
		if err := confirmRuntimeStopped(ctx, target); err != nil {
			return StopEnvironmentResult{}, err
		}
	}
	if decision.PersistIntent {
		if err := persistEnvironmentStopIntent(ctx, params.Result, params.SaveEnvConfig, true); err != nil {
			return StopEnvironmentResult{}, err
		}
	}
	ctx.Trace(fmt.Sprintf("==> Stopped %s/%s", target.Tenant, target.Environment))
	return result, nil
}

// confirmRuntimeStopped re-reads the Deployment after the scale so a stop that
// did not take effect is reported as a failure. Returning capacity is the whole
// point of the command, and a replica count that bounced back is the one
// outcome the operator cannot see for themselves — the config write that
// follows must never claim a stop the cluster did not keep.
func confirmRuntimeStopped(ctx Context, target RuntimeScaleTarget) error {
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

// traceAttachedAppSessions names the desktop terminal sessions living in the
// pod that is about to go away, and returns their ids for the structured
// result. Stopping ends them, so saying which turns tabs going dark into a
// stated consequence of the operator's own command.
func traceAttachedAppSessions(ctx Context, target RuntimeScaleTarget) []string {
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

// readAttachedAppSessions runs the same in-pod heartbeat probe the desktop uses
// to count live sessions. Like the run-state read it performs no mutation, so
// it runs under --dry-run too and the plan can show what a real stop would end.
func readAttachedAppSessions(ctx Context, target RuntimeScaleTarget) ([]RemoteAppSessionHeartbeat, error) {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "exec", "deployment/"+target.ReleaseName, "--", "/bin/sh", "-lc",
		RemoteAppSessionHeartbeatScript(target.Tenant, target.Environment))

	ctx.TraceCommand("", "kubectl", args...)
	output, err := Command("kubectl", args...).Output()
	if err != nil {
		if stderr := stderrFromExitError(err); stderr != "" {
			return nil, fmt.Errorf("%w: %s", err, stderr)
		}
		return nil, err
	}
	return ParseRemoteAppSessionHeartbeats(string(output)), nil
}

// ClearEnvironmentStopIntent records that the operator wants the environment
// running again, without touching the cluster. `open --deploy` calls it before
// helm renders the chart, so the rollout brings the pod up instead of
// re-applying the stop the config still remembered.
func ClearEnvironmentStopIntent(ctx Context, result OpenResult, saveEnvConfig func(string, EnvConfig) error) (EnvConfig, error) {
	if !result.EnvConfig.Stopped {
		return result.EnvConfig, nil
	}
	if err := persistEnvironmentStopIntent(ctx, result, saveEnvConfig, false); err != nil {
		return EnvConfig{}, err
	}
	updated := result.EnvConfig
	updated.Stopped = false
	return updated, nil
}

// WakeEnvironmentParams is a wake request: the resolved target plus the writer
// that clears the recorded stop intent. Reconnect marks the request as a
// supervisor reattaching a dropped session rather than an operator opening the
// environment, which is the difference between waking and leaving it stopped.
type WakeEnvironmentParams struct {
	Result        OpenResult
	SaveEnvConfig func(string, EnvConfig) error
	Reconnect     bool
}

// WakeEnvironmentResult reports what the wake did. EnvConfig carries the config
// forward when the stop intent was cleared, so the caller keeps working with the
// current values.
type WakeEnvironmentResult struct {
	EnvConfig      EnvConfig
	Woken          bool
	AlreadyRunning bool
}

// EnsureEnvironmentAwake brings a stopped environment back before anything that
// needs a running pod. `kubectl port-forward deployment/…` cannot attach to zero
// replicas, so this is what makes "start it when the operator opens it" work —
// and it is host-side, so an orchestrator that finds an environment stopped
// wakes it with `erun open` and no in-pod component is involved.
//
// A reconnecting caller gets the opposite answer for a stopped environment: an
// error, so the reattach fails plainly instead of quietly resurrecting what the
// operator stopped.
func EnsureEnvironmentAwake(ctx Context, params WakeEnvironmentParams) (WakeEnvironmentResult, error) {
	target := RuntimeScaleTargetForResult(params.Result)
	result := WakeEnvironmentResult{EnvConfig: params.Result.EnvConfig}

	// An unreadable run state must not abort the open. The wake exists to make
	// a stopped environment usable, so it can never be more fragile than the
	// open it precedes: presume the environment is running (the safe direction —
	// it never scales a healthy runtime away) and let the port-forwards and the
	// shell report the real problem, exactly as they did before the wake existed.
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

	// Say so before the wait: a cold wake pulls the pod back and can take a
	// minute, and a silent command looks hung.
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

// persistEnvironmentStopIntent records the operator's run/stop intent on the env
// config. It is what survives helm reconciliation: deploy renders the chart's
// `stopped` value from it, so an upgrade of a stopped env re-applies replicas: 0
// rather than restarting the pod behind the operator's back.
func persistEnvironmentStopIntent(ctx Context, result OpenResult, saveEnvConfig func(string, EnvConfig) error, stopped bool) error {
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

// runtimeRunStateSeparator joins the two jsonpath fields in one kubectl read so
// desired and ready replicas cannot be observed a moment apart.
const runtimeRunStateSeparator = "/"

// ReadRuntimeRunState reads the environment's runtime Deployment. Like
// CheckKubernetesDeployment it runs even under --dry-run: it performs no
// mutation, and stop/wake need the answer to resolve the plan they trace.
func ReadRuntimeRunState(ctx Context, target RuntimeScaleTarget) (RuntimeRunState, error) {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "get", "deployment", target.ReleaseName, "-o",
		"jsonpath={.spec.replicas}"+runtimeRunStateSeparator+"{.status.readyReplicas}")

	ctx.TraceCommand("", "kubectl", args...)
	rawOutput, err := Command("kubectl", args...).CombinedOutput()
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

// parseRuntimeRunState reads the `<desired>/<ready>` jsonpath pair. An empty
// ready field is kubectl's rendering of an unset value (a freshly-scaled
// Deployment reports no readyReplicas at all), which is zero, not an error.
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

// scaleRuntimeDeployment is the one mutation stop and wake share. It is traced
// before it runs and short-circuits under --dry-run, so the dry-run plan shows
// the exact command.
func scaleRuntimeDeployment(ctx Context, target RuntimeScaleTarget, replicas int) error {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "scale", "deployment/"+target.ReleaseName, "--replicas="+strconv.Itoa(replicas))
	return RunRawCommand(ctx, RawCommandSpec{Args: append([]string{"kubectl"}, args...)}, nil)
}

// WaitRuntimeAvailable blocks until the runtime Deployment reports Available,
// reusing the same rollout wait `erun open` performs before it execs a shell.
func WaitRuntimeAvailable(ctx Context, target RuntimeScaleTarget) error {
	args := kubectlRuntimeTargetArgs(target)
	args = append(args, "wait", "--for=condition=Available", "--timeout", defaultShellLaunchWaitTimeout, "deployment/"+target.ReleaseName)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return nil
	}
	if currentExecutionMode(kubectlDeploymentWaitExecutionOperation) == ExecutionModeLibrary {
		return libraryWaitForDeploymentAvailable(target.KubernetesContext, target.Namespace, target.ReleaseName, defaultShellLaunchWaitTimeout)
	}
	return RawCommandRunner("", "kubectl", args, ctx.Stdin, ctx.Stdout, ctx.Stderr)
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
