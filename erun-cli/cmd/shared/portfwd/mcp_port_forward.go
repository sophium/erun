package portfwd

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
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcp"
)

const McpPortForwardStartupTimeout = 5 * time.Second

type MCPForwarder func(core.Context, config.OpenResult) error

// Aliased so the CLI's and desktop's state shapes cannot drift.
type McpPortForwardState = model.PortForwardState

// ReusableRecordedPortForward reports whether the recorded forward still carries traffic,
// stopping it otherwise; a forward to a replaced pod still holds the port.
func ReusableRecordedPortForward(ctx core.Context, kind, logPath string, state, expected McpPortForwardState, localPort int, carriesTraffic func(int) bool) bool {
	bound := CanConnectLocalPort(localPort)
	matches := StateMatchesMCPTarget(state, expected)
	health := kube.ClassifyPortForward(matches, bound, bound && carriesTraffic(localPort))
	switch health {
	case kube.PortForwardServing:
		// A reused forward keeps its original log, so re-apply the cap on every touch.
		RotatePortForwardLogIfOversized(ctx, kind, logPath)
		return true
	case kube.PortForwardStale:
		ctx.Trace(fmt.Sprintf("%s: the port-forward on 127.0.0.1:%d holds the local port but its edge does not answer; re-establishing it", kind, localPort))
		ReapRecordedPortForwardProcess(matches, state.ProcessID, localPort)
	case kube.PortForwardDropped:
		if ReapRecordedPortForwardProcess(matches, state.ProcessID, localPort) {
			ctx.Trace(fmt.Sprintf("%s: the recorded port-forward for 127.0.0.1:%d never bound its port; clearing it (PID %d) before starting a fresh one", kind, localPort, state.ProcessID))
		}
	case kube.PortForwardUnestablished:
	}
	return false
}

// ReleaseUnreachablePortForward best-effort stops a forward that never became reachable,
// so it does not hold the port looking stale.
func ReleaseUnreachablePortForward(ctx core.Context, kind string, process *os.Process, localPort int, cause error) {
	ctx.Trace(fmt.Sprintf("%s: releasing port-forward for 127.0.0.1:%d — it never became reachable: %s", kind, localPort, strings.TrimSpace(cause.Error())))
	if process != nil {
		_ = process.Kill()
	}
	WaitForLocalPortToClose(localPort)
}

func McpPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return PortForwardStatePath("mcp", tenant, environment, dryRun)
}

func McpPortForwardLogPath(statePath string) string {
	return PortForwardLogPath(statePath)
}

func LoadMCPPortForwardState(path string) (McpPortForwardState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return McpPortForwardState{}, err
	}
	var state McpPortForwardState
	if err := json.Unmarshal(data, &state); err != nil {
		return McpPortForwardState{}, err
	}
	return state, nil
}

func SaveMCPPortForwardState(path string, state McpPortForwardState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func StateMatchesMCPTarget(state, expected McpPortForwardState) bool {
	return state.Tenant == expected.Tenant &&
		state.Environment == expected.Environment &&
		state.KubernetesContext == expected.KubernetesContext &&
		state.Namespace == expected.Namespace &&
		state.LocalPort == expected.LocalPort
}

func CanConnectLocalPort(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func CanReachLocalMCPEndpoint(port int) bool {
	return mcp.CanReachLocalMCPEndpoint(port)
}

func McpPortForwardTimeoutDetail(logPath string) string {
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
	case isListenConflictText(value):
		// The bind retry already waited this out, so another process really holds the port.
		return "the local port is held by a listener that is not releasing it; find and stop the process holding it, or free the port"
	default:
		return ""
	}
}

// stopPortForwardProcess kills pid only if the live process table still shows a kubectl
// port-forward, so reused PIDs are never touched; found reports that identification.
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

func WaitForLocalPortToClose(port int) {
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !CanConnectLocalPort(port) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
