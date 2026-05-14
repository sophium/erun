//go:build !windows

package cmd

import (
	"strconv"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// findLocalPortHolder reports the PID and full argv of the process holding a
// listener on 127.0.0.1:port, if it can be determined via the host's lsof + ps.
// Both macOS and Linux ship a compatible lsof, so the only cross-host risk is
// that lsof is not installed; in that case we behave as if no holder was
// found, and the caller falls back to the legacy "already in use" error path.
//
// Returning (0, nil, false) is reserved for "I could not determine a holder",
// not "the port is free". The caller has already established via
// canConnectLocalPort that the port is held; this function is strictly about
// identifying *who* holds it.
func findLocalPortHolder(port int) (int, []string, bool) {
	if port <= 0 {
		return 0, nil, false
	}
	out, err := eruncommon.Command("lsof", "-nP", "-iTCP:"+strconv.Itoa(port), "-sTCP:LISTEN", "-t").Output()
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
	out, err := eruncommon.Command("ps", "-ww", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil, false
	}
	command := strings.TrimSpace(string(out))
	if command == "" {
		return nil, false
	}
	// `ps -o command=` returns a single line per pid with space-separated
	// argv tokens. kubectl port-forward never embeds quoted spaces in any
	// of its args (paths can but namespaces/contexts/deployments cannot),
	// so a plain Fields split is safe here.
	return strings.Fields(command), true
}
