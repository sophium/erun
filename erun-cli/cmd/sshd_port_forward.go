package cmd

import (
	"encoding/json"
	"fmt"
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
	LogPath           string `json:"logPath,omitempty"`
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

	if stateMatchesSSHDTarget(state, expectedState) && canConnectLocalPort(info.Port) {
		return info, nil
	}
	if canConnectLocalPort(info.Port) {
		return common.SSHConnectionInfo{}, fmt.Errorf("local SSH port %d is already in use", info.Port)
	}

	args := kubectlPortForwardArgs(result, info.Port)
	ctx.TraceCommand("", "kubectl", args...)
	if ctx.DryRun {
		return info, nil
	}

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
	if err := cmd.Start(); err != nil {
		return common.SSHConnectionInfo{}, err
	}

	expectedState.LogPath = logPath
	if err := saveSSHDPortForwardState(statePath, expectedState); err != nil {
		return common.SSHConnectionInfo{}, err
	}

	deadline := time.Now().Add(sshdPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canConnectLocalPort(info.Port) {
			return info, nil
		}
		time.Sleep(100 * time.Millisecond)
	}

	return common.SSHConnectionInfo{}, fmt.Errorf("timed out waiting for SSH port-forward on 127.0.0.1:%d; see %s", info.Port, logPath)
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
