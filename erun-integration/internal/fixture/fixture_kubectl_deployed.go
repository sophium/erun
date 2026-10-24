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

// KubectlDeployedStubSpec describes the deployment shape the stubbed kubectl get
// deployment -o json should report so production's deployment-match check
// returns true and the open flow proceeds past the redeploy gate.
//
// The optional fields below extend the stub for real-run shell scenarios. Every
// zero value preserves the original silent-exit-0 behavior, so existing callers
// are unaffected.
type KubectlDeployedStubSpec struct {
	DeploymentName string
	ContainerName  string
	RepoPath       string
	SSHDEnabled    bool
	MCPPort        int
	SSHPort        int
	// ExecExitCodes, when non-empty, drives the interactive shell exec
	// branch (`kubectl ... exec -it deployment/... -- /bin/sh -lc <script>`):
	// the Nth call exits with the Nth code and the last code repeats for any
	// further calls. Calls are recorded one line per invocation in
	// <stubsDir>/exec-calls so tests can assert how many times the shell
	// loop re-entered kubectl exec (e.g. after a reattach-deploy handoff).
	ExecExitCodes []int
	// WaitExitCode, when non-zero, fails the deployment-availability wait
	// (`kubectl ... wait --for=condition=Available ... deployment/...`)
	// with this exit code after printing WaitStderr to stderr. Zero keeps
	// the wait succeeding silently.
	WaitExitCode int
	WaitStderr   string
	// PodsJSON, when non-empty, is written to <stubsDir>/pods.json and
	// returned verbatim for any `kubectl get pods ...` query. The open
	// flow's runtime diagnostics (`get pods -l app=<release> -o json`) and
	// the pod-replacement probe both read this shape.
	PodsJSON string
	// EventsJSON, when non-empty, is written to <stubsDir>/events.json and
	// returned verbatim for `kubectl get events -o json` (the runtime
	// diagnostics' per-pod warning-event lookup).
	EventsJSON string
	// SeedKeyFile, when non-empty, captures stdin of the non-interactive
	// SSH-key seeding call (`kubectl ... exec -i deployment/... -- /bin/sh
	// -c <seed-script>`) into this file so tests can assert the private key
	// was streamed on stdin and never via argv.
	SeedKeyFile string
	// DeploymentNotFound flips the deployment-check arms to report the
	// deployment as absent (NotFound on stderr, exit 1) instead of
	// present-and-matching, while keeping the port-forward simulator for
	// the forwards the open flow starts after its own deploy. Real-run
	// open scenarios use it to drive the deploy-then-forward path.
	DeploymentNotFound bool
	// FailingArgvSubstring, when non-empty, fails loudly (exit 1, a
	// distinctive stderr message naming the substring) for any kubectl
	// invocation whose argv contains it, before falling through to the rest
	// of this stub's arms. A library-execution-mode real-run scenario uses
	// this to prove a specific kubectl call was never reached as a
	// subprocess fallback -- e.g. "get deployment team-api -o name" for the
	// kubectl-deployment-get switch.
	FailingArgvSubstring string
	// ListenConflictAttempts, when non-zero, makes the port-forward
	// simulator fail the first N attempts to bind each local port the way
	// kubectl does when the port is still held by the process it replaced:
	// the two "Unable to listen" lines on stderr, exit 1. Later attempts run
	// the simulator normally, so a forward that retries its bind comes up. A
	// real-run scenario uses it to force the reattach race deterministically
	// instead of waiting for the host to produce it.
	ListenConflictAttempts int
}

// StubKubectlDeployed writes a kubectl stub that reports the named deployment as
// present-and-matching and runs the port-forward simulator for any port-forward
// invocation.
//
// The simulator processes are tracked via a PID file inside stubsDir so the
// t.Cleanup hook can reap them after the test; otherwise leftover listeners
// would hold the production ports across runs and break the next scenario with
// "local SSH port already in use".
func StubKubectlDeployed(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) []string {
	t.Helper()
	if err := os.MkdirAll(stubsDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", stubsDir, err)
	}
	// Forward-slash paths embedded into the sh stub body: this script runs
	// under Git Bash / Cygwin sh on Windows, where an unquoted backslash path
	// (e.g. the portsim invocation below) is mangled by backslash-as-escape and
	// the simulator never launches — production's port-forward reachability wait
	// then times out. Inert on Unix (ToSlash is a no-op).
	portsim := filepath.ToSlash(PortSimBinary(t))
	pidFile := filepath.ToSlash(filepath.Join(stubsDir, "portsim-pids"))
	deploymentJSON := fmt.Sprintf(`{"spec":{"template":{"spec":{"containers":[{"name":%q,"env":[{"name":"ERUN_REPO_PATH","value":%q},{"name":"ERUN_SSHD_ENABLED","value":%q},{"name":"ERUN_MCP_PORT","value":"%d"},{"name":"ERUN_SSHD_PORT","value":"%d"}],"resources":{"limits":{}}}]}}}}`,
		spec.ContainerName, spec.RepoPath, formatStubBool(spec.SSHDEnabled), spec.MCPPort, spec.SSHPort)
	script := strings.Join([]string{
		// Production passes the port-forward mapping as a single positional
		// after the resource reference (e.g. "deployment/team-devops
		// 17000:17000").
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
		// Production-side reachability checks differ by service:
		//   SSH expects the server to greet with a "SSH-" prefix
		//   MCP expects a successful HTTP response on GET /mcp
		//   API expects a 2xx HTTP response on GET /healthz
		// portsim writes the per-port banner to satisfy the SSH check; for
		// the MCP/API ports it answers the probe's HTTP request with a 200.
		// Only the SSH port needs a banner here.
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

// reapPortSims kills every simulator recorded in pidFile and waits for its
// listener to close.
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
		// SIGKILL is async: block until the simulator's listener is
		// actually released so a sibling real-run scenario reusing the
		// same fixed ports does not trip its port-busy guard on this
		// scenario's teardown.
		if portErr == nil && port > 0 {
			waitForPortClosed(port, 3*time.Second)
		}
	}
}

// kubectlListenConflictArm is the port-forward simulator's opening arm: when
// spec.ListenConflictAttempts is set, the first N attempts to bind each local
// port fail exactly the way kubectl does when the port is still held —
// "Unable to listen on port ..." and exit 1 — before the simulator proper
// takes over. It is the deterministic stand-in for a race the host cannot be
// asked to reproduce on demand: a replacement forward whose kubectl starts
// while the listener it replaced is still closing.
//
// The counter is per local port, so one scenario can drive all three forwards
// (MCP, sshd, API) through the same forced race without them racing each other
// for a single budget. Empty (the default) leaves the generated script
// byte-identical to what it was before this existed.
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

// kubectlDeployedOptionalArms emits the optional stub case arms
// most-specific-first: the interactive exec -it arm leads so the bootstrap
// script passed as its last argv can never fall through into the
// pods/events/wait arms by substring accident.
func kubectlDeployedOptionalArms(t testing.TB, stubsDir string, spec KubectlDeployedStubSpec) string {
	t.Helper()
	var arms strings.Builder
	// Forward-slash every path embedded into the sh stub body so Git Bash /
	// Cygwin sh on Windows reads them as paths, not escape sequences (ToSlash is
	// a no-op on Unix). See the rationale in StubKubectlDeployed.
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

// waitForPortClosed lets StubKubectlDeployed's teardown block until a killed
// port-forward simulator's listener is fully gone before the next scenario
// probes the same fixed port.
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
