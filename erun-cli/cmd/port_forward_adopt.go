package cmd

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	common "github.com/sophium/erun/erun-common"
)

// previewAdoptOrConflict mirrors in dry-run what the live path would do
// when the local port is already held: adopt the existing kubectl
// port-forward, replace it because it has gone stale, or refuse. previewed=true
// means a holder was found and the caller should short-circuit on the returned
// port; previewed=false means fall back to the normal kubectl-args trace.
// Without lsof no holder is ever found, so those platforms keep the old
// assumed-fresh-start plan.
//
// carriesTraffic is the service's own reachability probe. It is what stops the
// preview from promising an adoption of a forward whose far end is gone — the
// holder's argv proves only that it is the right shape, never that it works.
func previewAdoptOrConflict(ctx common.Context, kind string, localPort int, expectedArgs []string, carriesTraffic func(int) bool) (bool, int) {
	// Don't add a canConnectLocalPort/Dial gate here: lsof alone is
	// authoritative for "is anything listening", and a separate Dial would
	// make the probe sensitive to transient connect races.
	pid, argv, ok := findLocalPortHolder(localPort)
	if !ok {
		return false, 0
	}
	if argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
		if !holderCarriesTraffic(localPort, carriesTraffic) {
			ctx.Trace(fmt.Sprintf("%s: would stop the stale kubectl port-forward on 127.0.0.1:%d (PID %d) and start a fresh one — it holds the port but its edge never answers", kind, localPort, pid))
			return false, 0
		}
		ctx.Trace(fmt.Sprintf("%s: would adopt existing kubectl port-forward on 127.0.0.1:%d (PID %d)", kind, localPort, pid))
		return true, localPort
	}
	ctx.TraceCommand("", "kubectl", expectedArgs...)
	ctx.Trace(fmt.Sprintf("%s: would refuse to bind 127.0.0.1:%d — held by %s", kind, localPort, formatHolderForError(pid, argv)))
	return true, 0
}

// holderCarriesTraffic asks the service's probe whether the holder is more than
// a listener. A caller that supplies no probe cannot tell, and "cannot tell"
// must not read as "broken" — that would turn every adoption into a restart.
func holderCarriesTraffic(localPort int, carriesTraffic func(int) bool) bool {
	return carriesTraffic == nil || carriesTraffic(localPort)
}

// replaceStalePortForwardHolder stops a holder that is the very forward erun
// would start but no longer carries traffic, so the caller can start a fresh
// one, and reports whether the port actually came free. The kill is safe only
// because the caller has already matched the holder's argv against erun's own
// port-forward shape and stopPortForwardProcess re-checks that the PID is a
// kubectl port-forward: a port some other process legitimately holds is a
// conflict to report, never something to kill.
func replaceStalePortForwardHolder(ctx common.Context, kind string, pid, localPort int) bool {
	if _, err := stopPortForwardProcess(pid); err != nil {
		return false
	}
	waitForLocalPortToClose(localPort)
	if canConnectLocalPort(localPort) {
		return false
	}
	ctx.Trace(fmt.Sprintf("%s: stopped the stale kubectl port-forward on 127.0.0.1:%d (PID %d) — it held the port but its edge never answered", kind, localPort, pid))
	return true
}

// kubectlPortForwardArgv is a parsed view of a `kubectl ... port-forward ...`
// invocation, carrying only the fields used for adopt-or-conflict identity.
// Context is deliberately excluded from that comparison: a different
// `--context` alias can resolve to the same cluster, and adoption should
// still succeed.
type kubectlPortForwardArgv struct {
	Binary    string
	Context   string
	Namespace string
	Target    string
	PortPair  string
	Address   string
}

// parseKubectlPortForwardArgv interprets an argv slice as a
// `kubectl port-forward` invocation. The binary token matches any path
// whose basename starts with "kubectl" so version-managed launchers
// (`kuberlr`'s `kubectl1.32.0`, `kubectx` wrappers) still adopt.
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

// argvMatchesExpectedKubectlPortForward decides whether a foreign
// `kubectl port-forward` describes the same target erun would start itself.
// Namespace, target, or port-pair drift means a different tenant,
// environment, or service, so it is a hard "do not adopt". Context and
// address are intentionally excluded; see kubectlPortForwardArgv.Context.
func argvMatchesExpectedKubectlPortForward(actual []string, expected []string) bool {
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

// formatHolderForError describes the process holding a port erun wanted to
// bind, so the "already in use" error tells the user what to kill instead
// of a bare port number.
func formatHolderForError(pid int, argv []string) string {
	command := strings.Join(argv, " ")
	if command == "" {
		return ""
	}
	return "PID " + strconv.Itoa(pid) + ": " + command
}

// reapRecordedPortForwardProcess stops the process an environment's own
// state file last recorded, whether that forward is bound-but-dead or was
// never reached far enough to bind at all: bound state alone cannot tell a
// corpse that exited cleanly from one still running with nobody left to
// reap it, so the caller no longer gates this on the port being held.
// stopPortForwardProcess re-verifies the PID is still a kubectl port-forward
// before touching it, so a PID the OS has since reused is left alone.
// Returns whether a live process was actually found (and thus killed), so
// the caller can trace only when there was something to reap.
func reapRecordedPortForwardProcess(matches bool, processID, localPort int) bool {
	if !matches || processID <= 0 {
		return false
	}
	found, _ := stopPortForwardProcess(processID)
	waitForLocalPortToClose(localPort)
	return found
}

// previewClearRecordedPortForward traces the --dry-run equivalent of
// reapRecordedPortForward: naming a recorded PID that would be cleared
// because it never bound its port, without touching it. A bound port is a
// different decision the caller's own holder probe already owns (adopt or
// refuse), and is silent when there is nothing to report — the ordinary case
// of a state file whose process already exited cleanly.
func previewClearRecordedPortForward(ctx common.Context, kind string, matches bool, processID, localPort int) {
	if !matches || processID <= 0 || canConnectLocalPort(localPort) || !isPortForwardProcess(processID) {
		return
	}
	ctx.Trace(fmt.Sprintf("%s: would clear the recorded port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, processID))
}

// sweepDeadPortForwardsMatching kills every kubectl port-forward process on
// the host whose argv names this exact target/port pair but whose port is
// not currently bound. The state-file reap above only clears the PID the
// file itself remembers; two overlapping opens for the same environment can
// each read the file before either overwrites it, so the invocation that
// loses that race leaves a kubectl process with no state entry left pointing
// at it. Argv identity — the same shape adoption already matches on — is
// what still finds that one. Best-effort and unix-only
// (kubectlPortForwardProcessIDs is empty on Windows): a caller that cannot
// enumerate processes just skips the sweep, it does not fail the open.
func sweepDeadPortForwardsMatching(ctx common.Context, kind string, expectedArgs []string, localPort int) {
	if localPort <= 0 || canConnectLocalPort(localPort) {
		return
	}
	for _, pid := range kubectlPortForwardProcessIDs() {
		argv, ok := readProcessArgv(pid)
		if !ok || !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
			continue
		}
		if found, _ := stopPortForwardProcess(pid); found {
			ctx.Trace(fmt.Sprintf("%s: cleared an orphaned kubectl port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, pid))
		}
	}
}

// previewSweepDeadPortForwardsMatching mirrors sweepDeadPortForwardsMatching
// for --dry-run: it names any zombie it would clear without touching it.
func previewSweepDeadPortForwardsMatching(ctx common.Context, kind string, expectedArgs []string, localPort int) {
	if localPort <= 0 || canConnectLocalPort(localPort) {
		return
	}
	for _, pid := range kubectlPortForwardProcessIDs() {
		argv, ok := readProcessArgv(pid)
		if !ok || !argvMatchesExpectedKubectlPortForward(argv, expectedArgs) {
			continue
		}
		ctx.Trace(fmt.Sprintf("%s: would clear an orphaned kubectl port-forward for 127.0.0.1:%d (PID %d) — it never bound its port", kind, localPort, pid))
	}
}

// findLocalPortHolder identifies which process holds a port the caller has
// already confirmed is held. (0, nil, false) means the holder could not be
// determined — lsof/ps absent, or a Windows host, since adopting foreign
// port-forwards is a unix-only feature — not that the port is free; the caller
// then falls back to the legacy "already in use" plan. The host OS is resolved
// through common.DetectHost (not build tags) so the probe is a single
// command-based implementation on every platform and the ERUN_HOST_OS_OVERRIDE
// test seam can exercise the unix path on any host.
func findLocalPortHolder(port int) (int, []string, bool) {
	if port <= 0 || common.DetectHost().OS == common.HostOSWindows {
		return 0, nil, false
	}
	out, err := common.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
	if err != nil {
		return 0, nil, false
	}
	pidStr := strings.TrimSpace(string(out))
	if pidStr == "" {
		return 0, nil, false
	}
	// If multiple processes share the listener (rare for TCP listeners but
	// possible after a fork before bind teardown), keep the first.
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
	out, err := common.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, false
	}
	command := strings.TrimSpace(string(out))
	if command == "" {
		return nil, false
	}
	// kubectl port-forward never embeds spaces in its args (paths can, but
	// namespaces/contexts/deployments cannot), so a plain whitespace split is safe.
	return strings.Fields(command), true
}
