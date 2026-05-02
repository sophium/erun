package cmd

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
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

	if stateMatchesMCPTarget(state, expectedState) && canReachLocalAPIEndpoint(localPort) {
		return localPort, nil
	}
	stopStaleMCPPortForward(state, expectedState, localPort)
	if canConnectLocalPort(localPort) {
		return 0, fmt.Errorf("local API port %d is already in use", localPort)
	}

	args := kubectlAPIPortForwardArgs(result, localPort)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return localPort, nil
	}

	return startAPIPortForward(statePath, expectedState, args, localPort)
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

	cmd := exec.Command("kubectl", args...)
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
		"deployment/"+common.RuntimeReleaseName(result.Tenant),
		fmt.Sprintf("%d:%d", localPort, common.APIPortForResult(result)),
		"--address", "127.0.0.1",
	)
	return args
}

func apiPortForwardStatePath(tenant, environment string) (string, error) {
	path, err := mcpPortForwardStatePath(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(filepath.Dir(path)), "api", tenant, filepath.Base(path)), nil
}

func canReachLocalAPIEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/v1/whoami", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func apiPortForwardTimeoutDetail(logPath string) string {
	detail := mcpPortForwardTimeoutDetail(logPath)
	if detail == "" {
		return ""
	}
	return strings.ReplaceAll(detail, "MCP", "API")
}
