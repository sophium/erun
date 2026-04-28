package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

const mcpPortForwardStartupTimeout = 5 * time.Second

type MCPForwarder func(common.Context, common.OpenResult) error

type mcpPortForwardState struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	KubernetesContext string `json:"kubernetesContext"`
	Namespace         string `json:"namespace"`
	LocalPort         int    `json:"localPort"`
	LogPath           string `json:"logPath,omitempty"`
	ProcessID         int    `json:"processId,omitempty"`
}

func newMCPForwarder() MCPForwarder {
	return func(ctx common.Context, result common.OpenResult) error {
		_, err := ensureMCPPortForward(ctx, result)
		return err
	}
}

func ensureMCPPortForward(ctx common.Context, result common.OpenResult) (int, error) {
	localPort := common.MCPPortForResult(result)
	statePath, err := mcpPortForwardStatePath(result.Tenant, result.Environment)
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

	if stateMatchesMCPTarget(state, expectedState) && canReachLocalMCPEndpoint(localPort) {
		return localPort, nil
	}
	if stateMatchesMCPTarget(state, expectedState) && state.ProcessID > 0 && canConnectLocalPort(localPort) {
		_ = stopPortForwardProcess(state.ProcessID)
		waitForLocalPortToClose(localPort)
	}
	if canConnectLocalPort(localPort) {
		return 0, fmt.Errorf("local MCP port %d is already in use", localPort)
	}

	args := kubectlMCPPortForwardArgs(result, localPort)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return localPort, nil
	}

	logPath := mcpPortForwardLogPath(statePath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return 0, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return 0, err
	}

	expectedState.LogPath = logPath
	expectedState.ProcessID = cmd.Process.Pid
	if err := saveMCPPortForwardState(statePath, expectedState); err != nil {
		return 0, err
	}

	deadline := time.Now().Add(mcpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalMCPEndpoint(localPort) {
			return localPort, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if detail := mcpPortForwardTimeoutDetail(logPath); detail != "" {
		return 0, fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d: %s; see %s", localPort, detail, logPath)
	}
	return 0, fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d; see %s", localPort, logPath)
}

func kubectlMCPPortForwardArgs(result common.OpenResult, localPort int) []string {
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
		fmt.Sprintf("%d:%d", localPort, common.MCPPortForResult(result)),
		"--address", "127.0.0.1",
	)
	return args
}

func mcpPortForwardStatePath(tenant, environment string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "erun", "mcp", tenant, environment+".json"), nil
}

func mcpPortForwardLogPath(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
}

func loadMCPPortForwardState(path string) (mcpPortForwardState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpPortForwardState{}, err
	}
	var state mcpPortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return mcpPortForwardState{}, err
	}
	return state, nil
}

func saveMCPPortForwardState(path string, state mcpPortForwardState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func stateMatchesMCPTarget(state, expected mcpPortForwardState) bool {
	return state.Tenant == expected.Tenant &&
		state.Environment == expected.Environment &&
		state.KubernetesContext == expected.KubernetesContext &&
		state.Namespace == expected.Namespace &&
		state.LocalPort == expected.LocalPort
}

func canConnectLocalPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func canReachLocalMCPEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	client := http.Client{Timeout: 500 * time.Millisecond}
	resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/mcp", port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

func mcpPortForwardTimeoutDetail(logPath string) string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	value := strings.ToLower(string(data))
	switch {
	case strings.Contains(value, "pod not found"):
		return "runtime pod was replaced while connecting"
	case strings.Contains(value, "lost connection to pod") ||
		strings.Contains(value, "network namespace") ||
		strings.Contains(value, "sandbox"):
		return "runtime pod connection was lost, likely because the pod restarted"
	case strings.Contains(value, "connection refused"):
		return "runtime pod exists but MCP is not accepting connections yet"
	default:
		return ""
	}
}

func stopPortForwardProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if !isPortForwardProcess(pid) {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func waitForLocalPortToClose(port int) {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !canConnectLocalPort(port) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
