package cmd

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
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

	if stateMatchesSSHDTarget(state, expectedState) && !stateHasDeprecatedLocalProxy(state) && canReachLocalSSHEndpoint(info.Port) {
		return info, nil
	}
	stopStaleSSHDPortForward(state, expectedState, info.Port)
	if canConnectLocalPort(info.Port) {
		return common.SSHConnectionInfo{}, fmt.Errorf("local SSH port %d is already in use", info.Port)
	}

	args := kubectlPortForwardArgs(result, info.Port)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return info, nil
	}

	return startSSHDPortForward(statePath, expectedState, args, info)
}

func stopStaleSSHDPortForward(state, expectedState sshdPortForwardState, localPort int) {
	if !stateMatchesSSHDTarget(state, expectedState) || state.ProcessID <= 0 || !canConnectLocalPort(localPort) {
		return
	}
	stopSSHDPortForwardState(state)
	waitForLocalPortToClose(localPort)
	if state.ForwardPort > 0 {
		waitForLocalPortToClose(state.ForwardPort)
	}
}

func startSSHDPortForward(statePath string, expectedState sshdPortForwardState, args []string, info common.SSHConnectionInfo) (common.SSHConnectionInfo, error) {
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

	cmd := exec.Command("kubectl", args...)
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
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, "erun", "sshd", tenant, environment+".json"), nil
}

func sshdPortForwardLogPath(statePath string) string {
	return strings.TrimSuffix(statePath, filepath.Ext(statePath)) + ".log"
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

func stopSSHDPortForwardState(state sshdPortForwardState) {
	if state.ProxyProcessID > 0 {
		_ = stopSSHDActivityProxyProcess(state.ProxyProcessID)
	}
	if state.ProcessID > 0 {
		_ = stopPortForwardProcess(state.ProcessID)
	}
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
