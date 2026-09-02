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
	statePath, err := mcpPortForwardStatePath(result.Tenant, result.Environment, ctx.DryRun)
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
		if previewed, port := previewAdoptOrConflict(ctx, "mcp", localPort, args, canReachLocalMCPEndpoint); previewed {
			return port, nil
		}
		previewClearRecordedPortForward(ctx, "mcp", stateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
		previewSweepDeadPortForwardsMatching(ctx, "mcp", args, localPort)
		ctx.TraceCommand("", "kubectl", args...)
		return localPort, nil
	}

	if reusableRecordedPortForward(ctx, "mcp", state, expectedState, localPort, canReachLocalMCPEndpoint) {
		return localPort, nil
	}
	args := kubectlMCPPortForwardArgs(result, localPort)
	sweepDeadPortForwardsMatching(ctx, "mcp", args, localPort)
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

	return startMCPPortForward(ctx, statePath, expectedState, args, localPort)
}

// adoptForeignMCPPortForward reuses a pre-existing kubectl port-forward already
// targeting this env so repeated opens share one forward; a port held by any
// other process is a hard error rather than an adoption. A holder of erun's own
// shape that no longer carries traffic is neither: it is the forward that
// outlived its pod, so it is stopped and (false, nil) sends the caller on to
// start a replacement.
func adoptForeignMCPPortForward(ctx common.Context, statePath string, expected mcpPortForwardState, expectedArgs []string, localPort int) (bool, error) {
	pid, argv, ok := findLocalPortHolder(localPort)
	if !ok {
		return false, fmt.Errorf("local MCP port %d is already in use", localPort)
	}
	if !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local MCP port %d is already in use by %s", localPort, formatHolderForError(pid, argv))
	}
	if !canReachLocalMCPEndpoint(localPort) {
		if replaceStalePortForwardHolder(ctx, "mcp", pid, localPort) {
			return false, nil
		}
		return false, fmt.Errorf("local MCP port %d is held by a stale kubectl port-forward that could not be stopped: %s", localPort, formatHolderForError(pid, argv))
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

// reusableRecordedPortForward decides whether the forward already recorded for
// this environment can be reused, and stops it when it cannot. The listener and
// the tunnel behind it are separate facts: a forward whose pod was replaced
// keeps holding the local port and answers nothing through it, so reusing it on
// the strength of the recorded state alone leaves the environment unreachable
// with nothing left to notice.
func reusableRecordedPortForward(ctx common.Context, kind string, state, expected mcpPortForwardState, localPort int, carriesTraffic func(int) bool) bool {
	bound := canConnectLocalPort(localPort)
	matches := stateMatchesMCPTarget(state, expected)
	health := common.ClassifyPortForward(matches, bound, bound && carriesTraffic(localPort))
	switch health {
	case common.PortForwardServing:
		return true
	case common.PortForwardStale:
		ctx.Trace(fmt.Sprintf("%s: the port-forward on 127.0.0.1:%d holds the local port but its edge does not answer; re-establishing it", kind, localPort))
		reapRecordedPortForwardProcess(matches, state.ProcessID, localPort)
	case common.PortForwardDropped:
		if reapRecordedPortForwardProcess(matches, state.ProcessID, localPort) {
			ctx.Trace(fmt.Sprintf("%s: the recorded port-forward for 127.0.0.1:%d never bound its port; clearing it (PID %d) before starting a fresh one", kind, localPort, state.ProcessID))
		}
	}
	return false
}

func startMCPPortForward(ctx common.Context, statePath string, expectedState mcpPortForwardState, args []string, localPort int) (int, error) {
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
		releaseUnreachablePortForward(ctx, "mcp", cmd.Process, localPort, err)
		return 0, err
	}
	return localPort, nil
}

// releaseUnreachablePortForward stops a forward that was just started but
// never became reachable within its startup wait, so a slow or failed
// kubectl port-forward does not sit bound to the port claiming to be there —
// the "held but the edge never answers" shape that looks identical to a
// genuinely stale forward to the next caller. Best-effort: the process may
// already be gone, and the caller's own error already reports the failure.
func releaseUnreachablePortForward(ctx common.Context, kind string, process *os.Process, localPort int, cause error) {
	ctx.Trace(fmt.Sprintf("%s: releasing port-forward for 127.0.0.1:%d — it never became reachable: %s", kind, localPort, strings.TrimSpace(cause.Error())))
	if process != nil {
		_ = process.Kill()
	}
	waitForLocalPortToClose(localPort)
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

func mcpPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return portForwardStatePath("mcp", tenant, environment, dryRun)
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

// stopPortForwardProcess kills pid if, and only if, it is still identifiable
// as a live kubectl port-forward — isPortForwardProcess re-checks the live
// process table rather than trusting the caller's record, so a PID the OS
// has since reused for something else is never touched. found reports
// whether that identification succeeded (i.e. there was something alive to
// kill), independent of whether the kill itself errored.
func stopPortForwardProcess(pid int) (found bool, err error) {
	if pid <= 0 {
		return false, nil
	}
	if !isPortForwardProcess(pid) {
		return false, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return true, err
	}
	return true, process.Kill()
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
