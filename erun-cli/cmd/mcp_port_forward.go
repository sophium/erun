package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	common "github.com/sophium/erun/erun-common"
)

const mcpPortForwardStartupTimeout = 5 * time.Second

type MCPForwarder func(common.Context, common.OpenResult) error

// Aliased rather than redeclared so the shape the CLI writes and the shape the
// desktop reads cannot drift apart.
type mcpPortForwardState = common.PortForwardState

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

	if ctx.DryRun {
		args := kubectlMCPPortForwardArgs(result, localPort)
		if previewed, port := previewAdoptOrConflict(ctx, "mcp", localPort, args); previewed {
			return port, nil
		}
		ctx.TraceCommand("", "kubectl", args...)
		return localPort, nil
	}

	if stateMatchesMCPTarget(state, expectedState) && canReachLocalMCPEndpoint(localPort) {
		return localPort, nil
	}
	stopStaleMCPPortForward(state, expectedState, localPort)
	args := kubectlMCPPortForwardArgs(result, localPort)
	if canConnectLocalPort(localPort) {
		adopted, err := adoptForeignMCPPortForward(ctx, statePath, expectedState, args, localPort)
		if err != nil {
			return 0, err
		}
		if adopted {
			return localPort, nil
		}
	}

	ctx.TraceCommand("", "kubectl", args...)

	return startMCPPortForward(statePath, expectedState, args, localPort)
}

// adoptForeignMCPPortForward reuses a pre-existing kubectl port-forward already
// targeting this env so repeated opens share one forward; a port held by any
// other process is a hard error rather than an adoption.
func adoptForeignMCPPortForward(ctx common.Context, statePath string, expected mcpPortForwardState, expectedArgs []string, localPort int) (bool, error) {
	pid, argv, ok := findLocalPortHolder(localPort)
	if !ok {
		return false, fmt.Errorf("local MCP port %d is already in use", localPort)
	}
	if !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local MCP port %d is already in use by %s", localPort, formatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = mcpPortForwardLogPath(statePath)
	if err := saveMCPPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt MCP port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("mcp: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", localPort, pid))
	return true, nil
}

func stopStaleMCPPortForward(state, expectedState mcpPortForwardState, localPort int) {
	if !stateMatchesMCPTarget(state, expectedState) || state.ProcessID <= 0 || !canConnectLocalPort(localPort) {
		return
	}
	_ = stopPortForwardProcess(state.ProcessID)
	waitForLocalPortToClose(localPort)
}

func startMCPPortForward(statePath string, expectedState mcpPortForwardState, args []string, localPort int) (int, error) {
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

	cmd := common.Command("kubectl", args...)
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

	if err := waitForMCPPortForward(localPort, logPath); err != nil {
		return 0, err
	}
	return localPort, nil
}

func waitForMCPPortForward(localPort int, logPath string) error {
	deadline := time.Now().Add(mcpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalMCPEndpoint(localPort) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	if detail := mcpPortForwardTimeoutDetail(logPath); detail != "" {
		return fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d: %s; see %s", localPort, detail, logPath)
	}
	return fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d; see %s", localPort, logPath)
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
	return portForwardStatePath("mcp", tenant, environment)
}

func mcpPortForwardLogPath(statePath string) string {
	return portForwardLogPath(statePath)
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
	return common.CanReachLocalMCPEndpoint(port)
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
