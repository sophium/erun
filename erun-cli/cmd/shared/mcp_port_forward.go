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
	"github.com/sophium/erun/erun-common/infra/kube"
	"github.com/sophium/erun/erun-common/infra/mcp"
)

const McpPortForwardStartupTimeout = 5 * time.Second

type MCPForwarder func(core.Context, config.OpenResult) error

// Aliased rather than redeclared so the shape the CLI writes and the shape the
// desktop reads cannot drift apart.
type McpPortForwardState = kube.PortForwardState

// ReusableRecordedPortForward decides whether the forward already recorded for
// this environment can be reused, and stops it when it cannot. The listener and
// the tunnel behind it are separate facts: a forward whose pod was replaced
// keeps holding the local port and answers nothing through it, so reusing it on
// the strength of the recorded state alone leaves the environment unreachable
// with nothing left to notice.
func ReusableRecordedPortForward(ctx core.Context, kind, logPath string, state, expected McpPortForwardState, localPort int, carriesTraffic func(int) bool) bool {
	bound := CanConnectLocalPort(localPort)
	matches := StateMatchesMCPTarget(state, expected)
	health := kube.ClassifyPortForward(matches, bound, bound && carriesTraffic(localPort))
	switch health {
	case kube.PortForwardServing:
		// A serving forward is reused as-is and keeps writing to the log it
		// opened when it started, so this touch is the only chance to re-apply
		// the cap for as long as it stays up.
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

// ReleaseUnreachablePortForward stops a forward that was just started but
// never became reachable within its startup wait, so a slow or failed
// kubectl port-forward does not sit bound to the port claiming to be there —
// the "held but the edge never answers" shape that looks identical to a
// genuinely stale forward to the next caller. Best-effort: the process may
// already be gone, and the caller's own error already reports the failure.
func ReleaseUnreachablePortForward(ctx core.Context, kind string, process *os.Process, localPort int, cause error) {
	ctx.Trace(fmt.Sprintf("%s: releasing port-forward for 127.0.0.1:%d — it never became reachable: %s", kind, localPort, strings.TrimSpace(cause.Error())))
	if process != nil {
		_ = process.Kill()
	}
	waitForLocalPortToClose(localPort)
}

func McpPortForwardStatePath(tenant, environment string, dryRun bool) (string, error) {
	return PortForwardStatePath("mcp", tenant, environment, dryRun)
}

func McpPortForwardLogPath(statePath string) string {
	return portForwardLogPath(statePath)
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
		// The bind retry reads this same shape out of the log and waits the
		// socket out. Reaching here means it did not come free within the
		// retry window, so the port really is held by something else.
		return "the local port is held by a listener that is not releasing it; find and stop the process holding it, or free the port"
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
		if !CanConnectLocalPort(port) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
}
