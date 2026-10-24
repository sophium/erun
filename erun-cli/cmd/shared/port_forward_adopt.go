package shared

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
)

// PreviewAdoptOrConflict mirrors in dry-run the live adopt/replace/refuse decision for a
// held port; previewed=false means no holder was found. carriesTraffic is required
// because argv proves only the holder's shape, not that it works.
func PreviewAdoptOrConflict(ctx core.Context, kind string, localPort int, expectedArgs []string, carriesTraffic func(int) bool) (bool, int) {
	// No Dial gate: lsof is authoritative, and a Dial adds connect races.
	pid, argv, ok := FindLocalPortHolder(localPort)
	if !ok {
		return false, 0
	}
	if ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		if !holderCarriesTraffic(localPort, carriesTraffic) {
			ctx.Trace(fmt.Sprintf("%s: would stop the stale kubectl port-forward on 127.0.0.1:%d (PID %d) and start a fresh one — it holds the port but its edge never answers", kind, localPort, pid))
			return false, 0
		}
		ctx.Trace(fmt.Sprintf("%s: would adopt existing kubectl port-forward on 127.0.0.1:%d (PID %d)", kind, localPort, pid))
		return true, localPort
	}
	ctx.TraceCommand("", "kubectl", expectedArgs...)
	ctx.Trace(fmt.Sprintf("%s: would refuse to bind 127.0.0.1:%d — held by %s", kind, localPort, FormatHolderForError(pid, argv)))
	return true, 0
}

// holderCarriesTraffic treats a missing probe as working, so adoption is not turned into a restart.
func holderCarriesTraffic(localPort int, carriesTraffic func(int) bool) bool {
	return carriesTraffic == nil || carriesTraffic(localPort)
}

// ReplaceStalePortForwardHolder stops an argv-matched holder that no longer carries traffic
// and reports whether the port came free. Callers must have matched the argv first: a port
// held by any other process is a conflict to report, never something to kill.
func ReplaceStalePortForwardHolder(ctx core.Context, kind string, pid, localPort int) bool {
	if _, err := stopPortForwardProcess(pid); err != nil {
		return false
	}
	waitForLocalPortToClose(localPort)
	if CanConnectLocalPort(localPort) {
		return false
	}
	ctx.Trace(fmt.Sprintf("%s: stopped the stale kubectl port-forward on 127.0.0.1:%d (PID %d) — it held the port but its edge never answered", kind, localPort, pid))
	return true
}

// kubectlPortForwardArgv holds the fields used for adoption identity. Context is excluded:
// different aliases can name the same cluster.
type kubectlPortForwardArgv struct {
	Binary    string
	Context   string
	Namespace string
	Target    string
	PortPair  string
	Address   string
}

// parseKubectlPortForwardArgv accepts any binary whose basename starts with "kubectl", so versioned launchers adopt.
func parseKubectlPortForwardArgv(argv []string) (kubectlPortForwardArgv, bool) {
	if len(argv) == 0 || !strings.HasPrefix(filepath.Base(argv[0]), "kubectl") {
		return kubectlPortForwardArgv{}, false
	}
	out := kubectlPortForwardArgv{Binary: argv[0]}
	sawPortForward := false
	positionals := make([]string, 0, 4)
	for i := 1; i < len(argv); i++ {
		token := argv[i]
		if token == "port-forward" {
			sawPortForward = true
			continue
		}
		if consumed := consumeKubectlPortForwardFlag(argv, &i, &out); consumed {
			continue
		}
		if strings.HasPrefix(token, "-") {
			continue
		}
		positionals = append(positionals, token)
	}
	if !sawPortForward || len(positionals) < 2 {
		return kubectlPortForwardArgv{}, false
	}
	out.Target = positionals[0]
	out.PortPair = positionals[1]
	return out, true
}

func consumeKubectlPortForwardFlag(argv []string, i *int, out *kubectlPortForwardArgv) bool {
	for _, flag := range kubectlPortForwardIdentityFlags {
		value, advance, matched := matchKubectlFlag(argv, *i, flag.names)
		if !matched {
			continue
		}
		if advance > 0 {
			flag.assign(out, value)
			*i += advance - 1
		}
		return true
	}
	return false
}

type kubectlPortForwardIdentityFlag struct {
	names  []string
	assign func(*kubectlPortForwardArgv, string)
}

var kubectlPortForwardIdentityFlags = []kubectlPortForwardIdentityFlag{
	{names: []string{"--context"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Context = v }},
	{names: []string{"--namespace", "-n"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Namespace = v }},
	{names: []string{"--address"}, assign: func(o *kubectlPortForwardArgv, v string) { o.Address = v }},
}

func matchKubectlFlag(argv []string, i int, names []string) (value string, advance int, matched bool) {
	token := argv[i]
	for _, name := range names {
		if token == name {
			if i+1 >= len(argv) {
				return "", 0, true
			}
			return argv[i+1], 2, true
		}
		if strings.HasPrefix(token, name+"=") {
			return strings.TrimPrefix(token, name+"="), 1, true
		}
	}
	return "", 0, false
}

// ArgvMatchesExpectedKubectlPortForward reports whether a foreign port-forward has erun's namespace, target, and ports.
func ArgvMatchesExpectedKubectlPortForward(actual []string, expected []string) bool {
	a, ok := parseKubectlPortForwardArgv(actual)
	if !ok {
		return false
	}
	e, ok := parseKubectlPortForwardArgv(append([]string{"kubectl"}, expected...))
	if !ok {
		return false
	}
	return a.Namespace == e.Namespace && a.Target == e.Target && a.PortPair == e.PortPair
}

// FormatHolderForError describes the process holding a port for an "already in use" error.
func FormatHolderForError(pid int, argv []string) string {
	command := strings.Join(argv, " ")
	if command == "" {
		return ""
	}
	return "PID " + strconv.Itoa(pid) + ": " + command
}

// ReapRecordedPortForwardProcess stops the recorded forward whether or not it bound its port,
// re-verifying the PID first; it reports whether a live process was found.
func ReapRecordedPortForwardProcess(matches bool, processID, localPort int) bool {
	if !matches || processID <= 0 {
		return false
	}
	found, _ := stopPortForwardProcess(processID)
	waitForLocalPortToClose(localPort)
	return found
}

// PreviewClearRecordedPortForward traces, for --dry-run, a recorded PID that would be cleared because it never bound.
func PreviewClearRecordedPortForward(ctx core.Context, kind string, matches bool, processID, localPort int) {
	if !matches || processID <= 0 || CanConnectLocalPort(localPort) || !isPortForwardProcess(processID) {
		return
	}
	ctx.Trace(fmt.Sprintf("%s: would clear the recorded port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, processID))
}

// SweepDeadPortForwardsMatching kills port-forwards matching this target whose port is unbound,
// catching ones orphaned when overlapping opens overwrote the state file. Best-effort, unix-only.
func SweepDeadPortForwardsMatching(ctx core.Context, kind string, expectedArgs []string, localPort int) {
	if localPort <= 0 || CanConnectLocalPort(localPort) {
		return
	}
	for _, pid := range kubectlPortForwardProcessIDs() {
		argv, ok := readProcessArgv(pid)
		if !ok || !ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
			continue
		}
		if found, _ := stopPortForwardProcess(pid); found {
			ctx.Trace(fmt.Sprintf("%s: cleared an orphaned kubectl port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, pid))
		}
	}
}

// PreviewSweepDeadPortForwardsMatching names, for --dry-run, the processes the sweep would kill.
func PreviewSweepDeadPortForwardsMatching(ctx core.Context, kind string, expectedArgs []string, localPort int) {
	if localPort <= 0 || CanConnectLocalPort(localPort) {
		return
	}
	for _, pid := range kubectlPortForwardProcessIDs() {
		argv, ok := readProcessArgv(pid)
		if !ok || !ArgvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
			continue
		}
		ctx.Trace(fmt.Sprintf("%s: would clear an orphaned kubectl port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, pid))
	}
}

// FindLocalPortHolder identifies the process holding an already-held port. false means
// undetermined (no lsof/ps, or Windows), not free. The OS comes from DetectHost, not build
// tags, so ERUN_HOST_OS_OVERRIDE can exercise the unix path anywhere.
func FindLocalPortHolder(port int) (int, []string, bool) {
	if port <= 0 || core.DetectHost().OS == core.HostOSWindows {
		return 0, nil, false
	}
	out, err := core.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0, nil, false
	}
	pidStr := strings.TrimSpace(string(out))
	if pidStr == "" {
		return 0, nil, false
	}
	if newline := strings.IndexAny(pidStr, "\n\r"); newline > 0 {
		pidStr = pidStr[:newline]
	}
	pid, err := strconv.Atoi(strings.TrimSpace(pidStr))
	if err != nil || pid <= 0 {
		return 0, nil, false
	}
	argv, ok := readProcessArgv(pid)
	if !ok {
		return 0, nil, false
	}
	return pid, argv, true
}

func readProcessArgv(pid int) ([]string, bool) {
	if pid <= 0 {
		return nil, false
	}
	out, err := core.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, false
	}
	command := strings.TrimSpace(string(out))
	if command == "" {
		return nil, false
	}
	// kubectl port-forward args never contain spaces, so a whitespace split is safe.
	return strings.Fields(command), true
}
