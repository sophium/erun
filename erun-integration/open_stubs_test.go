package integration

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/fixture"
)

func netDialTimeout(network, address string, timeout time.Duration) (net.Conn, error) {
	return net.DialTimeout(network, address, timeout)
}

// skipIfPortsBusy is a last-resort guard for real-run scenarios: fixtures pin
// ports to the 26100 range, far from erun's default 17000, so this never
// fires on a developer machine with a live erun session.
func skipIfPortsBusy(t *testing.T, ports ...int) {
	t.Helper()
	for _, port := range ports {
		conn, err := netDialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			t.Skipf("port %d is already in use on this host; skipping real-run open scenario", port)
		}
	}
}

// openHostOSOverride pins DetectHost() to darwin for the open scenarios that
// cross a host-OS branch. Two branches need it, and both resolve through
// eruncommon.DetectHost (which honors this seam), so a single canonical OS keeps
// the Unix-recorded goldens deterministic on any host — including Windows, where
// they would otherwise diverge:
//   - the `open --no-shell` setup preamble dialect: POSIX
//     (`kubectl … >/dev/null && … cd '…'`) on unix vs PowerShell
//     (`… | Out-Null`, `Set-Location …`) when DetectHost reports windows
//     (erun-cli/cmd/open.go localShellSetupScript/detectOpenNoShellDialect);
//   - the adopt-or-conflict port-forward probe (findLocalPortHolder in
//     erun-cli/cmd/shared/port_forward_adopt.go), which is a no-op unless DetectHost
//     reports a unix host, so the lsof/ps decision-input stubs only drive it
//     under this override.
//
// It is a deliberate test seam, not a production knob (erun-integration/AGENTS.md
// § "Platform-dependent goldens"). Baked into the kubectl decision-input helpers
// below so every scenario that emits the preamble or drives the adopt probe pins
// it; SHELL-pinned scenarios (zsh/bash/pwsh) are unaffected because the shell
// name wins over host OS for the dialect.
const openHostOSOverride = "ERUN_HOST_OS_OVERRIDE=darwin"

// stubKubectlNotFound makes the deployment check a deterministic decision
// input. Without it, dry-run's redeploy branch is driven by whatever kubectl
// sits on the developer's PATH, leaking its "exit status 1" into the trace.
func stubKubectlNotFound(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `Error from server (NotFound): deployments.apps "team-devops" not found`,
		ExitCode: 1,
	})
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	return append(envVars, openHostOSOverride)
}

// portForwardStateFile is where production keeps a forward's record: under the
// config home the tenant config resolves through, on every host.
func portForwardStateFile(setup env.Setup, kind, tenant, environment string) string {
	return filepath.Join(setup.ConfigHome, "erun", "portforward", kind, tenant, environment+".json")
}

// legacyPortForwardStateFile is where an earlier release kept that record on
// macOS, beside os.UserConfigDir rather than the config home. Production keys
// that layout on the detected host, so a scenario pins it on any host with
// openHostOSOverride; the harness's XDG_CONFIG_HOME keeps it apart from the
// canonical tree.
func legacyPortForwardStateFile(setup env.Setup, kind, tenant, environment string) string {
	return filepath.Join(setup.Home, "Library", "Application Support", "erun", "portforward", kind, tenant, environment+".json")
}

// seedLegacyPortForwardRecord writes a record, its log and the log's rotated
// generation at the legacy layout, each carrying a marker line a scenario can
// look for after migration.
func seedLegacyPortForwardRecord(t *testing.T, setup env.Setup, kind, tenant, environment string, localPort int) string {
	t.Helper()
	statePath := legacyPortForwardStateFile(setup, kind, tenant, environment)
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatalf("mkdir legacy %s state dir: %v", kind, err)
	}
	body := fmt.Sprintf(
		`{"tenant":%q,"environment":%q,"kubernetesContext":"test-context","namespace":"%s-%s","localPort":%d}`,
		tenant, environment, tenant, environment, localPort)
	logPath := strings.TrimSuffix(statePath, ".json") + ".log"
	for path, content := range map[string]string{
		statePath:      body,
		logPath:        legacyPortForwardLogMarker + "\n",
		logPath + ".1": legacyPortForwardLogMarker + "\n",
	} {
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	return statePath
}

const legacyPortForwardLogMarker = "written before the upgrade"

// writePortForwardState writes the per-env forward record erun itself would
// have written, so a scenario can start from "erun already established this
// forward" without a first pass. processID must name a real process: production
// stops the recorded forward by that PID.
func writePortForwardState(t *testing.T, setup env.Setup, kind, tenant, environment string, localPort, processID int) {
	t.Helper()
	path := portForwardStateFile(setup, kind, tenant, environment)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s state dir: %v", kind, err)
	}
	body := fmt.Sprintf(
		`{"tenant":%q,"environment":%q,"kubernetesContext":"test-context","namespace":"%s-%s","localPort":%d,"processId":%d}`,
		tenant, environment, tenant, environment, localPort, processID)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write %s state: %v", kind, err)
	}
}

// adoptHolder describes one fake TCP port holder the lsof/ps probe stubs
// present to production's adopt-or-conflict check.
type adoptHolder struct {
	port int
	pid  int
	argv string
}

// stubAdoptHolderProbes presents a fake holder per configured port to
// production's adopt-or-conflict probe; unqueried ports report no holder so
// sibling-port probes stay silent. It returns nothing — the env vars are
// already wired by the kubectl helper since the stubs share its directory.
func stubAdoptHolderProbes(t *testing.T, setup env.Setup, holders ...adoptHolder) {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	lsofScript := `for arg in "$@"; do
    case "$arg" in
`
	for _, holder := range holders {
		lsofScript += fmt.Sprintf("        -iTCP:%d) printf '%%s\\n' '%d'; exit 0 ;;\n", holder.port, holder.pid)
	}
	lsofScript += `    esac
done
exit 1
`
	fixture.StubBinaryWithScript(t, stubs, "lsof", lsofScript)
	psScript := `pid=
prev=
for arg in "$@"; do
    if [ "$prev" = "-p" ]; then
        pid="$arg"
    fi
    prev="$arg"
done
case "$pid" in
`
	for _, holder := range holders {
		psScript += fmt.Sprintf("    %d) printf '%%s\\n' %s; exit 0 ;;\n", holder.pid, shellQuote(holder.argv))
	}
	psScript += `esac
exit 1
`
	fixture.StubBinaryWithScript(t, stubs, "ps", psScript)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// waitForFile polls because the side effects it reads are written by detached
// subprocesses (Start+Release launchers) that may outlive the erun run.
func waitForFile(t *testing.T, path string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("file %s did not appear with content within %s (last err: %v)", path, timeout, err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// stubKubectlGenericError returns an error that does NOT match the NotFound
// tokens, exercising the "check failed, assuming not deployed" fallback
// rather than the clean not-deployed branch.
func stubKubectlGenericError(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `Unable to connect to the server: dial tcp 10.0.0.1:443: i/o timeout`,
		ExitCode: 2,
	})
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	// Pin DetectHost so the --no-shell preamble dialect stays POSIX; see
	// openHostOSOverride.
	return append(envVars, openHostOSOverride)
}

// stubKubectlKlogFramedError reproduces erun#1766's real captured toast
// verbatim: client-go's klog "Unhandled Error" carrier around the same
// connection-refused cause stubKubectlGenericError uses, framed with the
// severity/timestamp/goroutine-id/source-location an operator can never act
// on.
func stubKubectlKlogFramedError(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `E0831 13:46:43.793908   10289 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"https://127.0.0.1:6443/api?timeout=32s\": dial tcp 127.0.0.1:6443: connect: connection refused"`,
		ExitCode: 1,
	})
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	return append(envVars, openHostOSOverride)
}

// stubLsofNoHolder keeps the adopt-or-conflict probe silent in scenarios that
// don't drive it, so a developer's leftover `kubectl port-forward` on an erun
// default port can't fire the probe mid-scenario and corrupt the golden.
func stubLsofNoHolder(t *testing.T, stubsDir string) {
	t.Helper()
	fixture.StubBinaryAdvanced(t, stubsDir, "lsof", fixture.StubBinarySpec{
		ExitCode: 1,
	})
	fixture.StubBinaryAdvanced(t, stubsDir, "ps", fixture.StubBinarySpec{
		ExitCode: 1,
	})
}

// stubKubectlRunState is stubKubectlNotFound's sibling for the wake path: it
// pins the runtime run-state read (the decision input for whether `open` must
// scale a stopped environment back up) instead of the deployment-presence
// check. Same host-OS override, for the same preamble/adopt-probe reasons.
func stubKubectlRunState(t *testing.T, setup env.Setup, desired, ready int) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlRuntimeRunState(t, stubs, desired, ready)
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	return append(envVars, openHostOSOverride)
}

// stubKubectlRunStateWithFailingWait is stubKubectlRunState's sibling for
// proving the wake path's kubectl-deployment-wait library dispatch
// (WaitRuntimeAvailable, stop.go) actually engages: it answers the run-state
// read like stubKubectlRunState, but fails loudly and distinctively for the
// "wait --for=condition=Available" argv shape instead of silently succeeding,
// so a fallback to the subprocess path surfaces as a recognizable failure.
//
// It also answers any `port-forward` invocation with a real listener, the
// same technique fixture.StubKubectlDeployed uses: open --no-shell has no
// shell to fall back on, so the post-wake MCP/API forwards this
// scenario's own run depends on must actually become reachable rather than
// the run timing out waiting on them. Spawned listener PIDs are recorded in
// stubsDir/portsim-pids so the returned cleanup can reap them.
func stubKubectlRunStateWithFailingWait(t *testing.T, stubsDir string, desired, ready int) {
	t.Helper()
	portsim := filepath.ToSlash(fixture.PortSimBinary(t))
	pidFile := filepath.ToSlash(filepath.Join(stubsDir, "portsim-pids"))
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
		`  ` + portsim + ` --port "$local_port" >/dev/null 2>&1 &`,
		`  pid=$!`,
		`  printf '%s %s\n' "$local_port" "$pid" >> '` + pidFile + `'`,
		`  wait "$pid"`,
		`  exit 0`,
		`fi`,
		`case "$*" in`,
		`  *jsonpath=*) printf '%s' '` + strconv.Itoa(desired) + `/` + strconv.Itoa(ready) + `'; exit 0 ;;`,
		`  *"wait --for=condition=Available"*)`,
		`    printf '%s\n' "fell through to the kubectl subprocess for the wait check" >&2`,
		`    exit 1 ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
	t.Cleanup(func() {
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
			if portErr == nil && port > 0 {
				fixture.StalePortHolderStopped(port, 3*time.Second)
			}
		}
	})
}
