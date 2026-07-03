//go:build !windows

package cmd

import (
	"strconv"
	"strings"

	eruncommon "github.com/sophium/erun/erun-common"
)

// findLocalPortHolder identifies which process holds a port the caller has
// already confirmed is held. (0, nil, false) means the holder could not be
// determined (e.g. lsof is absent), not that the port is free — the caller
// then falls back to the legacy "already in use" error.
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
	// kubectl port-forward never embeds spaces in its args (paths can, but
	// namespaces/contexts/deployments cannot), so a plain whitespace split is safe.
	return strings.Fields(command), true
}
