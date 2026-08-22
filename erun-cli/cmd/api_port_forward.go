package cmd

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
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
	statePath, err := apiPortForwardStatePath(result.Tenant, result.Environment)
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

	apiDeployment := common.TenantResourcePrefix(result.Tenant) + "-api"
	checkArgs := kubectlAPIDeploymentCheckArgs(expectedState.KubernetesContext, expectedState.Namespace, apiDeployment)
	ctx.TraceCommand("", "kubectl", checkArgs...)

	if ctx.DryRun {
		return ensureAPIPortForwardDryRun(ctx, result, localPort)
	}

	exists, err := checkAPIDeploymentPresent(checkArgs)
	if err != nil {
		return 0, err
	}
	if !exists {
		ctx.Trace(fmt.Sprintf("open: %s deployment not present in %s; skipping API port-forward", apiDeployment, expectedState.Namespace))
		stopStaleMCPPortForward(state, expectedState, localPort)
		return 0, nil
	}

	if reusableRecordedPortForward(ctx, "api", state, expectedState, localPort, canReachLocalAPIEndpoint) {
		return localPort, nil
	}
	args := kubectlAPIPortForwardArgs(result, localPort)
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

	return startAPIPortForward(statePath, expectedState, args, localPort)
}

// ensureAPIPortForwardDryRun previews the port-forward without a live cluster
// read: the real path (checkAPIDeploymentPresent, above) skips the whole
// forward when the tenant's <tenant>-api deployment is not present, but
// resolving that here would need every open dry-run scenario in the suite to
// declare a kubectl stub for a check it otherwise has no reason to. So the
// forward is stated as conditional on the presence check already traced by
// the caller, rather than asserted outright — the same split
// TraceEnsureKubernetesNamespace uses for the analogous namespace-create
// decision.
func ensureAPIPortForwardDryRun(ctx common.Context, result common.OpenResult, localPort int) (int, error) {
	args := kubectlAPIPortForwardArgs(result, localPort)
	if previewed, port := previewAdoptOrConflict(ctx, "api", localPort, args, canReachLocalAPIEndpoint); previewed {
		return port, nil
	}
	apiDeployment := common.TenantResourcePrefix(result.Tenant) + "-api"
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
	if err := saveMCPPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt API port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("api: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", localPort, pid))
	return true, nil
}

func kubectlAPIDeploymentCheckArgs(kubernetesContext, namespace, apiDeployment string) []string {
	args := make([]string, 0, 7)
	if strings.TrimSpace(kubernetesContext) != "" {
		args = append(args, "--context", kubernetesContext)
	}
	if strings.TrimSpace(namespace) != "" {
		args = append(args, "--namespace", namespace)
	}
	return append(args, "get", "deployment", apiDeployment, "-o", "name")
}

func checkAPIDeploymentPresent(args []string) (bool, error) {
	output, err := common.Command("kubectl", args...).CombinedOutput()
	if err == nil {
		return true, nil
	}
	message := strings.ToLower(string(output))
	if strings.Contains(message, "notfound") ||
		strings.Contains(message, "not found") ||
		strings.Contains(message, "no resources found") {
		return false, nil
	}
	return false, fmt.Errorf("failed to check API deployment: %w: %s", err, strings.TrimSpace(string(output)))
}

func startAPIPortForward(statePath string, expectedState mcpPortForwardState, args []string, localPort int) (int, error) {
	logPath := mcpPortForwardLogPath(statePath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := common.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	expectedState.ProcessID = cmd.Process.Pid
	if err := saveMCPPortForwardState(statePath, expectedState); err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	if err := waitForAPIPortForward(localPort, logPath); err != nil {
		_ = cmd.Process.Kill()
		return 0, err
	}
	return localPort, nil
}

func waitForAPIPortForward(localPort int, logPath string) error {
	deadline := time.Now().Add(mcpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalAPIEndpoint(localPort) {
			return nil
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
	args = append(args,
		"port-forward",
		fmt.Sprintf("service/%s-api", common.TenantResourcePrefix(result.Tenant)),
		// The API service is a standalone component chart, published on the
		// canonical APIServicePort in every namespace; only the local side is
		// per-env, so concurrent forwards for different environments don't collide
		// on the laptop. (MCP/SSH forward to the runtime pod, which is deployed on
		// per-env ports, so those map per-env on both sides.)
		fmt.Sprintf("%d:%d", localPort, common.APIServicePort),
		"--address", "127.0.0.1",
	)
	return args
}

func apiPortForwardStatePath(tenant, environment string) (string, error) {
	return portForwardStatePath("api", tenant, environment)
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
