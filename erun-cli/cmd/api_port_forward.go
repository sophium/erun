package cmd

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

type APIForwarder func(common.Context, common.OpenResult) error

func newAPIForwarder() APIForwarder {
	return func(ctx common.Context, result common.OpenResult) error {
		_, err := ensureAPIPortForward(ctx, result)
		return err
	}
}

func ensureAPIPortForward(ctx common.Context, result common.OpenResult) (int, error) {
	localPort := common.APIPortForResult(result)
	statePath, err := apiPortForwardStatePath(result.Tenant, result.Environment, ctx.DryRun)
	if err != nil {
		return 0, err
	}
	state, _ := loadMCPPortForwardState(statePath)
	expectedState := mcpPortForwardState{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		Namespace:         common.KubernetesNamespaceName(result.Tenant, result.Environment),
		LocalPort:         localPort,
	}

	apiDeployment := common.APIDeploymentName(result.Tenant)
	ctx.TraceCommand("", "kubectl", common.KubectlGetDeploymentArgs(expectedState.KubernetesContext, expectedState.Namespace, apiDeployment)...)

	if ctx.DryRun {
		return ensureAPIPortForwardDryRun(ctx, result, state, expectedState, localPort)
	}

	exists, err := common.DeploymentPresent(expectedState.KubernetesContext, expectedState.Namespace, apiDeployment)
	if err != nil {
		return 0, err
	}
	if !exists {
		ctx.Trace(fmt.Sprintf("open: %s deployment not present in %s; skipping API port-forward", apiDeployment, expectedState.Namespace))
		reapRecordedPortForwardProcess(stateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
		return 0, nil
	}

	if reusableRecordedPortForward(ctx, "api", mcpPortForwardLogPath(statePath), state, expectedState, localPort, canReachLocalAPIEndpoint) {
		return localPort, nil
	}
	args := kubectlAPIPortForwardArgs(result, localPort)
	sweepDeadPortForwardsMatching(ctx, "api", args, localPort)
	if canConnectLocalPort(localPort) {
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
func ensureAPIPortForwardDryRun(ctx common.Context, result common.OpenResult, state, expectedState mcpPortForwardState, localPort int) (int, error) {
	args := kubectlAPIPortForwardArgs(result, localPort)
	if previewed, port := previewAdoptOrConflict(ctx, "api", localPort, args, canReachLocalAPIEndpoint); previewed {
		return port, nil
	}
	previewClearRecordedPortForward(ctx, "api", stateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
	previewSweepDeadPortForwardsMatching(ctx, "api", args, localPort)
	apiDeployment := common.APIDeploymentName(result.Tenant)
	ctx.Trace(fmt.Sprintf("open: port-forwarding service/%s if the check above finds the deployment present", apiDeployment))
	return localPort, nil
}

// adoptForeignAPIPortForward mirrors adoptForeignMCPPortForward; see it for the adoption contract.
func adoptForeignAPIPortForward(ctx common.Context, statePath string, expected mcpPortForwardState, expectedArgs []string, localPort int) (bool, error) {
	pid, argv, ok := findLocalPortHolder(localPort)
	if !ok {
		return false, fmt.Errorf("local API port %d is already in use", localPort)
	}
	if !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local API port %d is already in use by %s", localPort, formatHolderForError(pid, argv))
	}
	if !canReachLocalAPIEndpoint(localPort) {
		if replaceStalePortForwardHolder(ctx, "api", pid, localPort) {
			return false, nil
		}
		return false, fmt.Errorf("local API port %d is held by a stale kubectl port-forward that could not be stopped: %s", localPort, formatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = mcpPortForwardLogPath(statePath)
	rotatePortForwardLogIfOversized(ctx, "api", adopted.LogPath)
	if err := saveMCPPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt API port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("api: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", localPort, pid))
	return true, nil
}

func startAPIPortForward(ctx common.Context, statePath string, expectedState mcpPortForwardState, args []string, localPort int) (int, error) {
	logPath := mcpPortForwardLogPath(statePath)
	expectedState.LogPath = logPath
	process, err := startPortForwardWithBindRetry(ctx, "api", localPort, func() (*os.Process, error) {
		logStart := portForwardLogSize(logPath)
		p, err := launchPortForwardProcessRetrying(func() (*os.Process, error) {
			return launchAPIPortForwardProcess(logPath, args)
		})
		if err != nil {
			return nil, err
		}
		expectedState.ProcessID = p.Pid
		if err := saveMCPPortForwardState(statePath, expectedState); err != nil {
			return p, err
		}
		if err := waitForAPIPortForward(localPort, logPath, logStart); err != nil {
			return p, err
		}
		return p, nil
	})
	if err != nil {
		releaseUnreachablePortForward(ctx, "api", process, localPort, err)
		return 0, err
	}
	return localPort, nil
}

func launchAPIPortForwardProcess(logPath string, args []string) (*os.Process, error) {
	logFile, err := openPortForwardLog(logPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := common.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// logStart is where this attempt's own output begins in logPath; see
// portForwardLogReportsListenConflict for why the caller takes it before
// launching rather than here.
func waitForAPIPortForward(localPort int, logPath string, logStart int64) error {
	deadline := time.Now().Add(mcpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalAPIEndpoint(localPort) {
			return nil
		}
		// See waitForMCPPortForward: kubectl exits on a failed listen, so the
		// conflict is readable from the log long before the startup timeout,
		// and the bind retry depends on being told promptly.
		if portForwardLogReportsListenConflict(logPath, logStart) {
			return listenConflictError(localPort, logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if detail := apiPortForwardTimeoutDetail(logPath); detail != "" {
		return fmt.Errorf("timed out waiting for API port-forward on 127.0.0.1:%d: %s; see %s", localPort, detail, logPath)
	}
	return fmt.Errorf("timed out waiting for API port-forward on 127.0.0.1:%d; see %s", localPort, logPath)
}

func kubectlAPIPortForwardArgs(result common.OpenResult, localPort int) []string {
	args := make([]string, 0, 8)
	if strings.TrimSpace(result.EnvConfig.KubernetesContext) != "" {
		args = append(args, "--context", result.EnvConfig.KubernetesContext)
	}
	namespace := common.KubernetesNamespaceName(result.Tenant, result.Environment)
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
	servicePort := common.APIPortForResult(result)
	args = append(args,
		"port-forward",
		fmt.Sprintf("service/%s", common.APIDeploymentName(result.Tenant)),
		fmt.Sprintf("%d:%d", localPort, servicePort),
		"--address", "127.0.0.1",
	)
	return args
}

func apiPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return portForwardStatePath("api", tenant, environment, dryRun)
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
	detail := mcpPortForwardTimeoutDetail(logPath)
	if detail == "" {
		return ""
	}
	return strings.ReplaceAll(detail, "MCP", "API")
}
