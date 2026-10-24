package apiserver

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/primitive/open"
)

func NewAPIForwarder() shared.APIForwarder {
	return func(ctx core.Context, result config.OpenResult) error {
		_, err := ensureAPIPortForward(ctx, result)
		return err
	}
}

func ensureAPIPortForward(ctx core.Context, result config.OpenResult) (int, error) {
	localPort := config.APIPortForResult(result)
	statePath, err := apiPortForwardStatePath(result.Tenant, result.Environment, ctx.DryRun)
	if err != nil {
		return 0, err
	}
	state, _ := shared.LoadMCPPortForwardState(statePath)
	expectedState := shared.McpPortForwardState{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		Namespace:         config.KubernetesNamespaceName(result.Tenant, result.Environment),
		LocalPort:         localPort,
	}

	apiDeployment := open.APIDeploymentName(result.Tenant)
	ctx.TraceCommand("", "kubectl", kube.KubectlGetDeploymentArgs(expectedState.KubernetesContext, expectedState.Namespace, apiDeployment)...)

	if ctx.DryRun {
		return ensureAPIPortForwardDryRun(ctx, result, state, expectedState, localPort)
	}

	exists, err := kube.DeploymentPresent(expectedState.KubernetesContext, expectedState.Namespace, apiDeployment)
	if err != nil {
		return 0, err
	}
	if !exists {
		ctx.Trace(fmt.Sprintf("open: %s deployment not present in %s; skipping API port-forward", apiDeployment, expectedState.Namespace))
		shared.ReapRecordedPortForwardProcess(shared.StateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
		return 0, nil
	}

	if shared.ReusableRecordedPortForward(ctx, "api", shared.McpPortForwardLogPath(statePath), state, expectedState, localPort, canReachLocalAPIEndpoint) {
		return localPort, nil
	}
	args := kubectlAPIPortForwardArgs(result, localPort)
	shared.SweepDeadPortForwardsMatching(ctx, "api", args, localPort)
	if shared.CanConnectLocalPort(localPort) {
		adopted, err := adoptForeignAPIPortForward(ctx, statePath, expectedState, args, localPort)
		if err != nil {
			return 0, err
		}
		if adopted {
			return localPort, nil
		}
	}

	ctx.TraceCommand("", "kubectl", args...)

	return startAPIPortForward(ctx, statePath, expectedState, args, localPort)
}

// ensureAPIPortForwardDryRun previews the port-forward without a live cluster
// read: the real path (common.DeploymentPresent, above) skips the whole
// forward when the tenant's <tenant>-api deployment is not present, but
// resolving that here would need every open dry-run scenario in the suite to
// declare a kubectl stub for a check it otherwise has no reason to. So the
// forward is stated as conditional on the presence check already traced by
// the caller, rather than asserted outright — the same split
// TraceEnsureKubernetesNamespace uses for the analogous namespace-create
// decision.
func ensureAPIPortForwardDryRun(ctx core.Context, result config.OpenResult, state, expectedState shared.McpPortForwardState, localPort int) (int, error) {
	args := kubectlAPIPortForwardArgs(result, localPort)
	if previewed, port := shared.PreviewAdoptOrConflict(ctx, "api", localPort, args, canReachLocalAPIEndpoint); previewed {
		return port, nil
	}
	shared.PreviewClearRecordedPortForward(ctx, "api", shared.StateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
	shared.PreviewSweepDeadPortForwardsMatching(ctx, "api", args, localPort)
	apiDeployment := open.APIDeploymentName(result.Tenant)
	ctx.Trace(fmt.Sprintf("open: port-forwarding service/%s if the check above finds the deployment present", apiDeployment))
	return localPort, nil
}

// adoptForeignAPIPortForward mirrors adoptForeignMCPPortForward; see it for the adoption contract.
func adoptForeignAPIPortForward(ctx core.Context, statePath string, expected shared.McpPortForwardState, expectedArgs []string, localPort int) (bool, error) {
	pid, argv, ok := shared.FindLocalPortHolder(localPort)
	if !ok {
		return false, fmt.Errorf("local API port %d is already in use", localPort)
	}
	if !shared.ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local API port %d is already in use by %s", localPort, shared.FormatHolderForError(pid, argv))
	}
	if !canReachLocalAPIEndpoint(localPort) {
		if shared.ReplaceStalePortForwardHolder(ctx, "api", pid, localPort) {
			return false, nil
		}
		return false, fmt.Errorf("local API port %d is held by a stale kubectl port-forward that could not be stopped: %s", localPort, shared.FormatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = shared.McpPortForwardLogPath(statePath)
	shared.RotatePortForwardLogIfOversized(ctx, "api", adopted.LogPath)
	if err := shared.SaveMCPPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt API port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("api: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", localPort, pid))
	return true, nil
}

func startAPIPortForward(ctx core.Context, statePath string, expectedState shared.McpPortForwardState, args []string, localPort int) (int, error) {
	logPath := shared.McpPortForwardLogPath(statePath)
	expectedState.LogPath = logPath
	process, err := shared.StartPortForwardWithBindRetry(ctx, "api", localPort, func() (*os.Process, error) {
		logStart := shared.PortForwardLogSize(logPath)
		p, err := shared.LaunchPortForwardProcessRetrying(func() (*os.Process, error) {
			return launchAPIPortForwardProcess(logPath, args)
		})
		if err != nil {
			return nil, err
		}
		expectedState.ProcessID = p.Pid
		if err := shared.SaveMCPPortForwardState(statePath, expectedState); err != nil {
			return p, err
		}
		if err := waitForAPIPortForward(localPort, logPath, logStart); err != nil {
			return p, err
		}
		return p, nil
	})
	if err != nil {
		shared.ReleaseUnreachablePortForward(ctx, "api", process, localPort, err)
		return 0, err
	}
	return localPort, nil
}

func launchAPIPortForwardProcess(logPath string, args []string) (*os.Process, error) {
	logFile, err := shared.OpenPortForwardLog(logPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := core.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	shared.DetachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// logStart is where this attempt's own output begins in logPath; see
// portForwardLogReportsListenConflict for why the caller takes it before
// launching rather than here.
func waitForAPIPortForward(localPort int, logPath string, logStart int64) error {
	deadline := time.Now().Add(shared.McpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalAPIEndpoint(localPort) {
			return nil
		}
		// See waitForMCPPortForward: kubectl exits on a failed listen, so the
		// conflict is readable from the log long before the startup timeout,
		// and the bind retry depends on being told promptly.
		if shared.PortForwardLogReportsListenConflict(logPath, logStart) {
			return shared.ListenConflictError(localPort, logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if detail := apiPortForwardTimeoutDetail(logPath); detail != "" {
		return fmt.Errorf("timed out waiting for API port-forward on 127.0.0.1:%d: %s; see %s", localPort, detail, logPath)
	}
	return fmt.Errorf("timed out waiting for API port-forward on 127.0.0.1:%d; see %s", localPort, logPath)
}

func kubectlAPIPortForwardArgs(result config.OpenResult, localPort int) []string {
	args := make([]string, 0, 8)
	if strings.TrimSpace(result.EnvConfig.KubernetesContext) != "" {
		args = append(args, "--context", result.EnvConfig.KubernetesContext)
	}
	namespace := config.KubernetesNamespaceName(result.Tenant, result.Environment)
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	// The far side is the environment's own API port, not the canonical
	// APIServicePort: `erun deploy` renders the erun-backend-api chart's
	// apiPort from this same per-env allocation (HelmDeploySpec.APIPort, out
	// of LocalPortsForResult), so an environment allocated 17300 publishes its
	// API Service on 17333. Pinning the far side to 17033 asks kubectl for a
	// service port the Service does not expose, and the whole forward fails
	// for every environment outside the 17000-17099 range. The local side
	// staying per-env is what keeps concurrent forwards for different
	// environments from colliding on the laptop, and it is why the two sides
	// carry the same number here rather than one being rewritten to a
	// constant: MCP and SSH forward the same way.
	//
	// APIServicePortForResult carries deploy's own fallback with it: an
	// environment that resolves no port block at all deploys the chart's
	// `default 17033`, so that is the port its Service answers on and the one
	// to ask kubectl for.
	servicePort := config.APIServicePortForResult(result)
	args = append(args,
		"port-forward",
		fmt.Sprintf("service/%s", open.APIDeploymentName(result.Tenant)),
		fmt.Sprintf("%d:%d", localPort, servicePort),
		"--address", "127.0.0.1",
	)
	return args
}

func apiPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return shared.PortForwardStatePath("api", tenant, environment, dryRun)
}

func canReachLocalAPIEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/healthz", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

func apiPortForwardTimeoutDetail(logPath string) string {
	detail := shared.McpPortForwardTimeoutDetail(logPath)
	if detail == "" {
		return ""
	}
	return strings.ReplaceAll(detail, "MCP", "API")
}
