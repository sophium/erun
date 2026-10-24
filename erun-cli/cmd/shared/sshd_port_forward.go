package shared

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/open"
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

func EnsureSSHDPortForward(ctx core.Context, result config.OpenResult) (model.SSHConnectionInfo, error) {
	info := open.SSHConnectionInfoForResult(result)
	statePath, err := sshdPortForwardStatePath(result.Tenant, result.Environment, ctx.DryRun)
	if err != nil {
		return model.SSHConnectionInfo{}, err
	}
	state, _ := loadSSHDPortForwardState(statePath)
	expectedState := sshdPortForwardState{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		Namespace:         config.KubernetesNamespaceName(result.Tenant, result.Environment),
		LocalPort:         info.Port,
	}

	matches := stateMatchesSSHDTarget(state, expectedState)
	if matches && !stateHasDeprecatedLocalProxy(state) && canReachLocalSSHEndpoint(info.Port) {
		// A reused forward keeps its original log, so re-apply the cap here.
		RotatePortForwardLogIfOversized(ctx, "sshd", sshdPortForwardLogPath(statePath))
		return info, nil
	}
	args := kubectlPortForwardArgs(result, info.Port)
	if ctx.DryRun {
		PreviewClearRecordedPortForward(ctx, "sshd", matches, state.ProcessID, info.Port)
		PreviewSweepDeadPortForwardsMatching(ctx, "sshd", args, info.Port)
		if previewed, _ := PreviewAdoptOrConflict(ctx, "sshd", info.Port, args, canReachLocalSSHEndpoint); previewed {
			return info, nil
		}
		ctx.TraceCommand("", "kubectl", args...)
		return info, nil
	}
	stopStaleSSHDPortForward(ctx, matches, state, info.Port)
	SweepDeadPortForwardsMatching(ctx, "sshd", args, info.Port)
	if CanConnectLocalPort(info.Port) {
		adopted, err := adoptForeignSSHDPortForward(ctx, statePath, expectedState, args, info)
		if err != nil {
			return model.SSHConnectionInfo{}, err
		}
		if adopted {
			return info, nil
		}
	}

	ctx.TraceCommand("", "kubectl", args...)

	return startSSHDPortForward(ctx, statePath, expectedState, args, info)
}

// adoptForeignSSHDPortForward is the SSHD counterpart of adoptForeignMCPPortForward.
func adoptForeignSSHDPortForward(ctx core.Context, statePath string, expected sshdPortForwardState, expectedArgs []string, info model.SSHConnectionInfo) (bool, error) {
	pid, argv, ok := FindLocalPortHolder(info.Port)
	if !ok {
		return false, fmt.Errorf("local SSH port %d is already in use", info.Port)
	}
	if !ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local SSH port %d is already in use by %s", info.Port, FormatHolderForError(pid, argv))
	}
	if !canReachLocalSSHEndpoint(info.Port) {
		if ReplaceStalePortForwardHolder(ctx, "sshd", pid, info.Port) {
			return false, nil
		}
		return false, fmt.Errorf("local SSH port %d is held by a stale kubectl port-forward that could not be stopped: %s", info.Port, FormatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = sshdPortForwardLogPath(statePath)
	RotatePortForwardLogIfOversized(ctx, "sshd", adopted.LogPath)
	if err := saveSSHDPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt SSHD port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("sshd: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", info.Port, pid))
	return true, nil
}

// stopStaleSSHDPortForward stops this env's recorded forward before replacement. A deprecated
// legacy record is replaced regardless, so it skips the "doesn't answer" trace.
func stopStaleSSHDPortForward(ctx core.Context, matches bool, state sshdPortForwardState, localPort int) {
	if !matches || state.ProcessID <= 0 {
		return
	}
	bound := CanConnectLocalPort(localPort)
	if bound && !stateHasDeprecatedLocalProxy(state) {
		ctx.Trace(fmt.Sprintf("sshd: the port-forward on 127.0.0.1:%d holds the local port but its edge does not answer; re-establishing it", localPort))
	}
	found := ReapRecordedPortForwardProcess(matches, state.ProcessID, localPort)
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

func startSSHDPortForward(ctx core.Context, statePath string, expectedState sshdPortForwardState, args []string, info model.SSHConnectionInfo) (model.SSHConnectionInfo, error) {
	logPath := sshdPortForwardLogPath(statePath)
	expectedState.LogPath = logPath
	process, err := StartPortForwardWithBindRetry(ctx, "sshd", info.Port, func() (*os.Process, error) {
		logStart := PortForwardLogSize(logPath)
		p, err := LaunchPortForwardProcessRetrying(func() (*os.Process, error) {
			return launchSSHDPortForwardProcess(logPath, args)
		})
		if err != nil {
			return nil, err
		}
		expectedState.ProcessID = p.Pid
		if err := saveSSHDPortForwardState(statePath, expectedState); err != nil {
			return p, err
		}
		if err := waitForSSHDPortForward(info.Port, logPath, logStart); err != nil {
			return p, err
		}
		return p, nil
	})
	if err != nil {
		ReleaseUnreachablePortForward(ctx, "sshd", process, info.Port, err)
		return model.SSHConnectionInfo{}, err
	}
	return info, nil
}

func launchSSHDPortForwardProcess(logPath string, args []string) (*os.Process, error) {
	logFile, err := OpenPortForwardLog(logPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := core.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	DetachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// logStart must be taken by the caller before launching kubectl.
func waitForSSHDPortForward(port int, logPath string, logStart int64) error {
	deadline := time.Now().Add(sshdPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if canReachLocalSSHEndpoint(port) {
			return nil
		}
		// kubectl exits on a failed listen, so report the conflict promptly for the bind retry.
		if PortForwardLogReportsListenConflict(logPath, logStart) {
			return ListenConflictError(port, logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}

	return fmt.Errorf("timed out waiting for SSH port-forward on 127.0.0.1:%d; see %s", port, logPath)
}

func kubectlPortForwardArgs(result config.OpenResult, localPort int) []string {
	args := make([]string, 0, 8)
	if strings.TrimSpace(result.EnvConfig.KubernetesContext) != "" {
		args = append(args, "--context", result.EnvConfig.KubernetesContext)
	}
	namespace := config.KubernetesNamespaceName(result.Tenant, result.Environment)
	if namespace != "" {
		args = append(args, "--namespace", namespace)
	}
	args = append(args,
		"port-forward",
		"deployment/"+config.RuntimeReleaseName(result.Tenant),
		fmt.Sprintf("%d:%d", localPort, config.SSHLocalPortForResult(result)),
		"--address", "127.0.0.1",
	)
	return args
}

func sshdPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return PortForwardStatePath("sshd", tenant, environment, dryRun)
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
