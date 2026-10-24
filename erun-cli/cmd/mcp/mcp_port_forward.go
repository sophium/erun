package mcp

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/sophium/erun/cmd/shared"
	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func NewMCPForwarder() shared.MCPForwarder {
	return func(ctx core.Context, result config.OpenResult) error {
		_, err := ensureMCPPortForward(ctx, result)
		return err
	}
}

func ensureMCPPortForward(ctx core.Context, result config.OpenResult) (int, error) {
	localPort := config.MCPPortForResult(result)
	statePath, err := shared.McpPortForwardStatePath(result.Tenant, result.Environment, ctx.DryRun)
	if err != nil {
		return 0, err
	}
	state, _ := shared.LoadMCPPortForwardState(statePath)
	expectedState := shared.McpPortForwardState{
		Tenant:            result.Tenant,
		Environment:       result.Environment,
		KubernetesContext: strings.TrimSpace(result.EnvConfig.KubernetesContext),
		Namespace:         config.KubernetesNamespaceName(result.Tenant, result.Environment),
		LocalPort:         localPort,
	}

	if ctx.DryRun {
		args := kubectlMCPPortForwardArgs(result, localPort)
		if previewed, port := shared.PreviewAdoptOrConflict(ctx, "mcp", localPort, args, shared.CanReachLocalMCPEndpoint); previewed {
			return port, nil
		}
		shared.PreviewClearRecordedPortForward(ctx, "mcp", shared.StateMatchesMCPTarget(state, expectedState), state.ProcessID, localPort)
		shared.PreviewSweepDeadPortForwardsMatching(ctx, "mcp", args, localPort)
		ctx.TraceCommand("", "kubectl", args...)
		return localPort, nil
	}

	if shared.ReusableRecordedPortForward(ctx, "mcp", shared.McpPortForwardLogPath(statePath), state, expectedState, localPort, shared.CanReachLocalMCPEndpoint) {
		return localPort, nil
	}
	args := kubectlMCPPortForwardArgs(result, localPort)
	shared.SweepDeadPortForwardsMatching(ctx, "mcp", args, localPort)
	if shared.CanConnectLocalPort(localPort) {
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

// adoptForeignMCPPortForward reuses an existing forward for this env, errors on a
// foreign holder, and stops a dead erun forward so the caller starts a new one.
func adoptForeignMCPPortForward(ctx core.Context, statePath string, expected shared.McpPortForwardState, expectedArgs []string, localPort int) (bool, error) {
	pid, argv, ok := shared.FindLocalPortHolder(localPort)
	if !ok {
		return false, fmt.Errorf("local MCP port %d is already in use", localPort)
	}
	if !shared.ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		return false, fmt.Errorf("local MCP port %d is already in use by %s", localPort, shared.FormatHolderForError(pid, argv))
	}
	if !shared.CanReachLocalMCPEndpoint(localPort) {
		if shared.ReplaceStalePortForwardHolder(ctx, "mcp", pid, localPort) {
			return false, nil
		}
		return false, fmt.Errorf("local MCP port %d is held by a stale kubectl port-forward that could not be stopped: %s", localPort, shared.FormatHolderForError(pid, argv))
	}
	adopted := expected
	adopted.ProcessID = pid
	adopted.LogPath = shared.McpPortForwardLogPath(statePath)
	shared.RotatePortForwardLogIfOversized(ctx, "mcp", adopted.LogPath)
	if err := shared.SaveMCPPortForwardState(statePath, adopted); err != nil {
		return false, fmt.Errorf("adopt MCP port-forward (PID %d): %w", pid, err)
	}
	ctx.Trace(fmt.Sprintf("mcp: adopted existing kubectl port-forward on 127.0.0.1:%d (PID %d)", localPort, pid))
	return true, nil
}

func startMCPPortForward(ctx core.Context, statePath string, expectedState shared.McpPortForwardState, args []string, localPort int) (int, error) {
	logPath := shared.McpPortForwardLogPath(statePath)
	expectedState.LogPath = logPath
	process, err := shared.StartPortForwardWithBindRetry(ctx, "mcp", localPort, func() (*os.Process, error) {
		logStart := shared.PortForwardLogSize(logPath)
		p, err := shared.LaunchPortForwardProcessRetrying(func() (*os.Process, error) {
			return launchMCPPortForwardProcess(logPath, args)
		})
		if err != nil {
			return nil, err
		}
		expectedState.ProcessID = p.Pid
		if err := shared.SaveMCPPortForwardState(statePath, expectedState); err != nil {
			return p, err
		}
		if err := waitForMCPPortForward(localPort, logPath, logStart); err != nil {
			return p, err
		}
		return p, nil
	})
	if err != nil {
		shared.ReleaseUnreachablePortForward(ctx, "mcp", process, localPort, err)
		return 0, err
	}
	return localPort, nil
}

func launchMCPPortForwardProcess(logPath string, args []string) (*os.Process, error) {
	logFile, err := shared.OpenPortForwardLog(logPath)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = logFile.Close()
	}()

	cmd := core.Command("kubectl", args...)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	shared.DetachBackgroundProcess(cmd)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd.Process, nil
}

// logStart is where this attempt's output begins in logPath; the caller takes it before launching.
func waitForMCPPortForward(localPort int, logPath string, logStart int64) error {
	deadline := time.Now().Add(shared.McpPortForwardStartupTimeout)
	for time.Now().Before(deadline) {
		if shared.CanReachLocalMCPEndpoint(localPort) {
			return nil
		}
		// kubectl exits on a failed listen; only this attempt's log output counts.
		if shared.PortForwardLogReportsListenConflict(logPath, logStart) {
			return shared.ListenConflictError(localPort, logPath)
		}
		time.Sleep(100 * time.Millisecond)
	}

	if detail := shared.McpPortForwardTimeoutDetail(logPath); detail != "" {
		return fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d: %s; see %s", localPort, detail, logPath)
	}
	return fmt.Errorf("timed out waiting for MCP port-forward on 127.0.0.1:%d; see %s", localPort, logPath)
}

func kubectlMCPPortForwardArgs(result config.OpenResult, localPort int) []string {
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
		fmt.Sprintf("%d:%d", localPort, config.MCPPortForResult(result)),
		"--address", "127.0.0.1",
	)
	return args
}
