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

const sshdPortForwardStartupTimeout = 5 * time.Second

type sshdPortForwardState struct {
	Tenant            string `json:"tenant"`
	Environment       string `json:"environment"`
	KubernetesContext string `json:"kubernetesContext"`
	Namespace         string `json:"namespace"`
	LocalPort         int    `json:"localPort"`
	ForwardPort       int    `json:"forwardPort,omitempty"`
	LogPath           string `json:"logPath,omitempty"`
	ProcessID         int    `json:"processId,omitempty"`
	ProxyProcessID    int    `json:"proxyProcessId,omitempty"`
}

func ensureSSHDPortForward(ctx common.Context, result common.OpenResult) (common.SSHConnectionInfo, error) {
	info := common.SSHConnectionInfoForResult(result)
	statePath, err := sshdPortForwardStatePath(result.Tenant, result.Environment)
	if err != nil {
		return common.SSHConnectionInfo{}, err
	}
	state, _ := loadSSHDPortForwardState(statePath)
	expectedState := sshdPortForwardState{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		Namespace:         common.KubernetesNamespaceName(result.Tenant, result.Environment),
		LocalPort:         info.Port,
	}

	matches := stateMatchesSSHDTarget(state, expectedState)
	if matches && !stateHasDeprecatedLocalProxy(state) && canReachLocalSSHEndpoint(info.Port) {
		return info, nil
	}
	args := kubectlPortForwardArgs(result, info.Port)
	if ctx.DryRun {
		previewClearRecordedPortForward(ctx, "sshd", matches, state.ProcessID, info.Port)
		previewSweepDeadPortForwardsMatching(ctx, "sshd", args, info.Port)
		if previewed, _ := previewAdoptOrConflict(ctx, "sshd", info.Port, args, canReachLocalSSHEndpoint); previewed {
			return info, nil
		}
		ctx.TraceCommand("", "kubectl", args...)
		return info, nil
	}
	stopStaleSSHDPortForward(ctx, matches, state, info.Port)
	sweepDeadPortForwardsMatching(ctx, "sshd", args, info.Port)
	if canConnectLocalPort(info.Port) {
		adopted, err := adoptForeignSSHDPortForward(ctx, statePath, expectedState, args, info)
		if err != nil {
			return common.SSHConnectionInfo{}, err
		}
		if adopted {
			return info, nil
		}
	}

	ctx.TraceCommand("", "kubectl", args...)

	return startSSHDPortForward(ctx, statePath, expectedState, args, info)
}

// adoptForeignSSHDPortForward mirrors adoptForeignMCPPortForward for the
// SSHD forward. See that function for the contract.
func adoptForeignSSHDPortForward(ctx common.Context, statePath string, expected sshdPortForwardState, expectedArgs []string, info common.SSHConnectionInfo) (bool, error) {
	pid, argv, ok := findLocalPortHolder(info.Port)
	if !ok {
		return false, fmt.Errorf("local SSH port %d is already in use", info.Port)
	}
	if !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local SSH port %d is already in use by %s", info.Port, formatHolderForError(pid, argv))
	}
	if !canReachLocalSSHEndpoint(info.Port) {
		if replaceStalePortForwardHolder(ctx, "sshd", pid, info.Port) {
			return false, nil
		}
		return false, fmt.Errorf("local SSH port %d is held by a stale kubectl port-forward that could not be stopped: %s", info.Port, formatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = sshdPortForwardLogPath(statePath)
	if err := saveSSHDPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt SSHD port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("sshd: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", info.Port, pid))
	return true, nil
}

// stopStaleSSHDPortForward stops the process behind this env's own recorded
// forward before a fresh one replaces it — whether that forward is
// bound-but-dead or was never reached far enough to bind at all, since bound
// state alone cannot tell a corpse that exited cleanly from one still
// running with nobody left to reap it. deprecated (a legacy local-proxy
// record) forces replacement regardless of reachability, so it is excluded
// from the "holds the port but doesn't answer" trace, which would otherwise
// misdescribe a forward that actually answers fine.
func stopStaleSSHDPortForward(ctx common.Context, matches bool, state sshdPortForwardState, localPort int) {
	if !matches || state.ProcessID <= 0 {
		return
	}
	bound := canConnectLocalPort(localPort)
	if bound && !stateHasDeprecatedLocalProxy(state) {
		ctx.Trace(fmt.Sprintf("sshd: the port-forward on 127.0.0.1:%d holds the local port but its edge does not answer; re-establishing it", localPort))
	}
	found := reapRecordedPortForwardProcess(matches, state.ProcessID, localPort)
	if !bound && found {
		ctx.Trace(fmt.Sprintf("sshd: the recorded port-forward for 127.0.0.1:%d never bound its port; clearing it (PID %d) before starting a fresh one", localPort, state.ProcessID))
	}
	if state.ProxyProcessID > 0 {
		_ = stopSSHDActivityProxyProcess(state.ProxyProcessID)
	}
	if state.ForwardPort > 0 {
		waitForLocalPortToClose(state.ForwardPort)
	}
}

func startSSHDPortForward(ctx common.Context, statePath string, expectedState sshdPortForwardState, args []string, info common.SSHConnectionInfo) (common.SSHConnectionInfo, error) {
	logPath := sshdPortForwardLogPath(statePath)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return common.SSHConnectionInfo{}, err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return common.SSHConnectionInfo{}, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := common.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	detachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return common.SSHConnectionInfo{}, err
	}

	expectedState.LogPath = logPath
	expectedState.ProcessID = cmd.Process.Pid
	if err := saveSSHDPortForwardState(statePath, expectedState); err != nil {
		return common.SSHConnectionInfo{}, err
	}

	if err := waitForSSHDPortForward(info.Port, logPath); err != nil {
		releaseUnreachablePortForward(ctx, "sshd", cmd.Process, info.Port, err)
		return common.SSHConnectionInfo{}, err
	}
	return info, nil
}

func waitForSSHDPortForward(port int, logPath string) error {
	deadline := time.Now().Add(sshdPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalSSHEndpoint(port) {
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for SSH port-forward on 127.0.0.1:%d; see %s", port, logPath)
}

func kubectlPortForwardArgs(result common.OpenResult, localPort int) []string {
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
		fmt.Sprintf("%d:%d", localPort, common.SSHLocalPortForResult(result)),
		"--address", "127.0.0.1",
	)
	return args
}

func sshdPortForwardStatePath(tenant, environment string) (string, error) {
	return portForwardStatePath("sshd", tenant, environment)
}

func sshdPortForwardLogPath(statePath string) string {
	return portForwardLogPath(statePath)
}

func loadSSHDPortForwardState(path string) (sshdPortForwardState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return sshdPortForwardState{}, err
	}
	var state sshdPortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return sshdPortForwardState{}, err
	}
	return state, nil
}

func saveSSHDPortForwardState(path string, state sshdPortForwardState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func stateMatchesSSHDTarget(state, expected sshdPortForwardState) bool {
	return state.Tenant == expected.Tenant &&
		state.Environment == expected.Environment &&
		state.KubernetesContext == expected.KubernetesContext &&
		state.Namespace == expected.Namespace &&
		state.LocalPort == expected.LocalPort
}

func stateHasDeprecatedLocalProxy(state sshdPortForwardState) bool {
	return state.ForwardPort > 0 || state.ProxyProcessID > 0
}

func stopSSHDActivityProxyProcess(pid int) error {
	if pid <= 0 {
		return nil
	}
	if !isSSHDActivityProxyProcess(pid) {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func canReachLocalSSHEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 500*time.Millisecond)
	if err != nil {
		return false
	}
	defer func() {
		_ = conn.Close()
	}()
	if err := conn.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		return false
	}
	buffer := make([]byte, 4)
	n, err := conn.Read(buffer)
	return err == nil && n >= 4 && string(buffer[:4]) == "SSH-"
}
