package fixture

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// KubectlDeployedStubSpec is the deployment the kubectl stub reports as present and matching.
// Zero-valued optional fields keep the plain silent exit 0.
type KubectlDeployedStubSpec struct {
	DeploymentName string
	ContainerName  string
	RepoPath       string
	SSHDEnabled    bool
	MCPPort        int
	SSHPort        int
	// ExecExitCodes drives `exec -it` calls in order, repeating the last; calls are logged to <stubsDir>/exec-calls.
	ExecExitCodes []int
	// WaitExitCode, when non-zero, fails the Available wait after printing WaitStderr.
	WaitExitCode int
	WaitStderr   string
	// PodsJSON answers any `get pods` query.
	PodsJSON string
	// EventsJSON answers `get events -o json`.
	EventsJSON string
	// SeedKeyFile captures the key-seeding exec's stdin, proving the key never travels in argv.
	SeedKeyFile string
	// DeploymentNotFound reports the deployment absent while still simulating port-forwards.
	DeploymentNotFound bool
	// FailingArgvSubstring fails any call containing it, proving a library-mode run never shelled out for it.
	FailingArgvSubstring string
	// ListenConflictAttempts fails the first N binds per local port like a still-held port, forcing the reattach race.
	ListenConflictAttempts int
}

// StubKubectlDeployed writes the kubectl stub and runs the port-forward simulator for port-forwards.
// Cleanup reaps simulators so their listeners don't hold fixed ports into the next scenario.
func StubKubectlDeployed(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) []string {
	t.Helper()
	if err := os.MkdirAll(stubsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", stubsDir, err)
	}
	// Forward slashes: Git Bash sh on Windows reads backslashes as escapes, so the portsim would never launch.
	portsim := filepath.ToSlash(PortSimBinary(t))
	pidFile := filepath.ToSlash(filepath.Join(stubsDir, "portsim-pids"))
	deploymentJSON := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[{"name":"ERUN_REPO_PATH","value":%q},{"name":"ERUN_SSHD_ENABLED","value":%q},{"name":"ERUN_MCP_PORT","value":"%d"},{"name":"ERUN_SSHD_PORT","value":"%d"}],"resources":{"limits":{}}}]}}}}`,
		spec.ContainerName, spec.RepoPath, formatStubBool(spec.SSHDEnabled), spec.MCPPort, spec.SSHPort)
	script := strings.Join([]string{
		`is_port_forward=0`,
		`local_port=""`,
		`for arg in "$@"; do`,
		`  case "$arg" in`,
		`    port-forward) is_port_forward=1 ;;`,
		`    *:*)`,
		`      if [ "$is_port_forward" = "1" ]; then`,
		`        case "$arg" in`,
		`          *[!0-9:]*) : ;;`,
		`          *) local_port="${arg%%:*}" ;;`,
		`        esac`,
		`      fi ;;`,
		`  esac`,
		`done`,
		`if [ "$is_port_forward" = "1" ] && [ -n "$local_port" ]; then`,
		kubectlListenConflictArm(spec, stubsDir),
		// Only the SSH port needs a banner; portsim answers MCP/API HTTP probes with 200.
		`  banner=""`,
		`  if [ "$local_port" = "` + strconv.Itoa(spec.SSHPort) + `" ]; then`,
		`    banner="SSH-2.0-erun-portsim\r\n"`,
		`  fi`,
		`  ` + portsim + ` --port "$local_port" --banner "$banner" >/dev/null 2>&1 &`,
		`  pid=$!`,
		`  printf '%s %s\n' "$local_port" "$pid" >> '` + pidFile + `'`,
		`  # Stay alive while the simulator runs so production code that`,
		`  # treats the kubectl process as the port-forward owner sees it`,
		`  # as live. Block until the simulator exits.`,
		`  wait "$pid"`,
		`  exit 0`,
		`fi`,
		`# Argv-driven branching for non-port-forward kubectl invocations.`,
		`case "$*" in`,
	}, "\n")
	script += "\n" + kubectlDeployedOptionalArms(t, stubsDir, spec)
	script += kubectlDeploymentGetArms(spec, deploymentJSON)
	StubBinaryWithScript(t, stubsDir, "kubectl", script)
	t.Cleanup(func() { reapPortSims(pidFile) })
	return StubEnv(stubsDir, "kubectl")
}

func kubectlDeploymentGetArms(spec KubectlDeployedStubSpec, deploymentJSON string) string {
	if spec.DeploymentNotFound {
		return strings.Join([]string{
			`  *"get deployment ` + spec.DeploymentName + ` -o name"*)`,
			`    printf '%s\n' 'Error from server (NotFound): deployments.apps "` + spec.DeploymentName + `" not found' >&2`,
			`    exit 1 ;;`,
			`  *) : ;;`,
			`esac`,
			`exit 0`,
		}, "\n")
	}
	return strings.Join([]string{
		`  *"get deployment ` + spec.DeploymentName + ` -o name"*)`,
		`    printf 'deployment.apps/%s\n' '` + spec.DeploymentName + `' ;;`,
		`  *"get deployment ` + spec.DeploymentName + ` -o json"*)`,
		`    printf '%s' '` + deploymentJSON + `' ;;`,
		`  *) : ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
}

func reapPortSims(pidFile string) {
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		port, portErr := strconv.Atoi(fields[0])
		pid, pidErr := strconv.Atoi(fields[1])
		if pidErr != nil || pid <= 0 {
			continue
		}
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
		// SIGKILL is async; wait until the listener is released for the next scenario.
		if portErr == nil && port > 0 {
			waitForPortClosed(port, 3*time.Second)
		}
	}
}

// kubectlListenConflictArm counts attempts per local port so each forward gets its own budget.
func kubectlListenConflictArm(spec KubectlDeployedStubSpec, stubsDir string) string {
	if spec.ListenConflictAttempts <= 0 {
		return ""
	}
	counterFor := filepath.ToSlash(filepath.Join(stubsDir, "listen-conflict-attempts-"))
	return strings.Join([]string{
		`  conflict_counter='` + counterFor + `'"$local_port"`,
		`  conflict_attempts=0`,
		`  if [ -f "$conflict_counter" ]; then conflict_attempts=$(wc -l < "$conflict_counter" | tr -d '[:space:]'); fi`,
		`  printf 'attempt\n' >> "$conflict_counter"`,
		`  if [ "$conflict_attempts" -lt ` + strconv.Itoa(spec.ListenConflictAttempts) + ` ]; then`,
		`    printf 'Unable to listen on port %s: Listeners failed to create with the following errors: [unable to create listener: Error listen tcp4 127.0.0.1:%s: bind: address already in use]\n' "$local_port" "$local_port" >&2`,
		`    printf 'error: unable to listen on any of the requested ports: [{%s %s}]\n' "$local_port" "$local_port" >&2`,
		`    exit 1`,
		`  fi`,
	}, "\n")
}

// kubectlDeployedOptionalArms emits the most specific arms first so the exec -it script argv cannot
// match a later arm by substring.
func kubectlDeployedOptionalArms(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) string {
	t.Helper()
	var arms strings.Builder
	if spec.FailingArgvSubstring != "" {
		arms.WriteString(`  *"` + spec.FailingArgvSubstring + `"*)` + "\n")
		arms.WriteString(`    printf '%s\n' "fell through to the kubectl subprocess for the ` + spec.FailingArgvSubstring + ` check" >&2` + "\n")
		arms.WriteString(`    exit 1 ;;` + "\n")
	}
	if len(spec.ExecExitCodes) > 0 {
		counterFile := filepath.ToSlash(filepath.Join(stubsDir, "exec-calls"))
		arms.WriteString(`  *" exec -it "*)` + "\n")
		arms.WriteString(`    count=0` + "\n")
		arms.WriteString(`    if [ -f '` + counterFile + `' ]; then count=$(wc -l < '` + counterFile + `' | tr -d '[:space:]'); fi` + "\n")
		arms.WriteString(`    printf 'call\n' >> '` + counterFile + `'` + "\n")
		arms.WriteString(`    case "$count" in` + "\n")
		for index, code := range spec.ExecExitCodes[:len(spec.ExecExitCodes)-1] {
			fmt.Fprintf(&arms, `      %d) exit %d ;;`+"\n", index, code)
		}
		fmt.Fprintf(&arms, `      *) exit %d ;;`+"\n", spec.ExecExitCodes[len(spec.ExecExitCodes)-1])
		arms.WriteString(`    esac ;;` + "\n")
	}
	if spec.SeedKeyFile != "" {
		arms.WriteString(`  *" exec -i deployment/"*)` + "\n")
		arms.WriteString(`    cat > '` + filepath.ToSlash(spec.SeedKeyFile) + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	if spec.WaitExitCode != 0 {
		arms.WriteString(`  *" wait --for=condition=Available"*)` + "\n")
		if spec.WaitStderr != "" {
			arms.WriteString(`    printf '%s\n' ` + shellSingleQuote(spec.WaitStderr) + ` >&2` + "\n")
		}
		fmt.Fprintf(&arms, `    exit %d ;;`+"\n", spec.WaitExitCode)
	}
	if spec.PodsJSON != "" {
		podsFile := filepath.ToSlash(filepath.Join(stubsDir, "pods.json"))
		mustWrite(t, podsFile, spec.PodsJSON)
		arms.WriteString(`  *" get pods "*)` + "\n")
		arms.WriteString(`    cat '` + podsFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	if spec.EventsJSON != "" {
		eventsFile := filepath.ToSlash(filepath.Join(stubsDir, "events.json"))
		mustWrite(t, eventsFile, spec.EventsJSON)
		arms.WriteString(`  *" get events "*)` + "\n")
		arms.WriteString(`    cat '` + eventsFile + `'` + "\n")
		arms.WriteString(`    exit 0 ;;` + "\n")
	}
	return arms.String()
}

func waitForPortClosed(port int, timeout time.Duration) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err != nil {
			return
		}
		_ = conn.Close()
		time.Sleep(50 * time.Millisecond)
	}
}

func formatStubBool(value bool) string {
	if value {
		return "true"
	}
	return "false"
}
