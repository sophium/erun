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

// skipIfPortsBusy is a last-resort guard; fixtures use the 26100 range, away from erun's 17000.
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

// openHostOSOverride pins DetectHost to darwin so the --no-shell preamble dialect and
// the unix-only adopt probe keep goldens deterministic on any host.
const openHostOSOverride = "ERUN_HOST_OS_OVERRIDE=darwin"

// stubKubectlNotFound keeps the developer's own kubectl out of dry-run's redeploy decision.
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

func portForwardStateFile(setup env.Setup, kind, tenant, environment string) string {
	return filepath.Join(setup.ConfigHome, "erun", "portforward", kind, tenant, environment+".json")
}

// legacyPortForwardStateFile is the old macOS layout beside os.UserConfigDir.
func legacyPortForwardStateFile(setup env.Setup, kind, tenant, environment string) string {
	return filepath.Join(setup.Home, "Library", "Application Support", "erun", "portforward", kind, tenant, environment+".json")
}

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

// writePortForwardState seeds the forward record erun would write; processID must be a
// real process because production stops the forward by PID.
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

type adoptHolder struct {
	port int
	pid  int
	argv string
}

// stubAdoptHolderProbes fakes a holder per port; other ports report none.
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

// waitForFile polls because detached subprocesses may write after erun exits.
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

// stubKubectlGenericError returns a non-NotFound error to hit the "assuming not deployed" fallback.
func stubKubectlGenericError(t *testing.T, setup env.Setup) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubBinaryAdvanced(t, stubs, "kubectl", fixture.StubBinarySpec{
		Stderr:   `Unable to connect to the server: dial tcp 10.0.0.1:443: i/o timeout`,
		ExitCode: 2,
	})
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	return append(envVars, openHostOSOverride)
}

// stubKubectlKlogFramedError wraps the connection-refused cause in client-go's klog framing.
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

// stubLsofNoHolder stops a developer's leftover port-forward from firing the adopt probe.
func stubLsofNoHolder(t *testing.T, stubsDir string) {
	t.Helper()
	fixture.StubBinaryAdvanced(t, stubsDir, "lsof", fixture.StubBinarySpec{
		ExitCode: 1,
	})
	fixture.StubBinaryAdvanced(t, stubsDir, "ps", fixture.StubBinarySpec{
		ExitCode: 1,
	})
}

// stubKubectlRunState pins the run-state read that decides whether open scales the env back up.
func stubKubectlRunState(t *testing.T, setup env.Setup, desired, ready int) []string {
	t.Helper()
	stubs := setup.Cwd + "/stubs"
	fixture.StubKubectlRuntimeRunState(t, stubs, desired, ready)
	stubLsofNoHolder(t, stubs)
	envVars := append(setup.Env(), fixture.StubEnv(stubs, "kubectl", "lsof", "ps")...)
	return append(envVars, openHostOSOverride)
}

// stubKubectlRunStateWithFailingWait fails the `wait --for=condition=Available` argv loudly,
// proving the library wait is used; port-forwards get real listeners (PIDs in
// stubsDir/portsim-pids) because --no-shell has no shell to fall back on.
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
