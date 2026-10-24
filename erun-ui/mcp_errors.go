package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	"github.com/sophium/erun/erun-common/infra/mcp"
)

// errMCPUnreachable makes the frontend offer Reconnect; recovery can redeploy,
// so it only runs on that explicit user action.
var errMCPUnreachable = errors.New("mcp unreachable")

// mcpUnreachableMarker prefixes surfaced errors so the frontend detects the
// state independent of locale or message text. Never shown to users.
const mcpUnreachableMarker = "ERUN_MCP_UNREACHABLE: "

// mcpUnreachableKindMarkers carry the reachability kind: stopped is
// informational, a stale forward is a fault.
var mcpUnreachableKindMarkers = map[mcp.LocalMCPUnreachableKind]string{
	mcp.LocalMCPNotOpen:      "ERUN_MCP_UNREACHABLE_NOT_OPEN: ",
	mcp.LocalMCPStaleForward: "ERUN_MCP_UNREACHABLE_STALE: ",
}

func wrapMCPUnreachableError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s%w: %w", mcpUnreachableMarker, errMCPUnreachable, err)
}

// wrapMCPUnreachableErrorWithKind also tags the already-classified reachability kind.
func wrapMCPUnreachableErrorWithKind(kind mcp.LocalMCPUnreachableKind, err error) error {
	if err == nil {
		return nil
	}
	marker, ok := mcpUnreachableKindMarkers[kind]
	if !ok {
		return wrapMCPUnreachableError(err)
	}
	return fmt.Errorf("%s%w: %w", marker, errMCPUnreachable, err)
}

func isMCPDialFailure(err error) bool {
	if err == nil {
		return false
	}
	var netErr *net.OpError
	if errors.As(err, &netErr) {
		return true
	}
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ETIMEDOUT) {
		return true
	}
	return mcpDialFailureMessage(err.Error())
}

func mcpDialFailureMessage(msg string) bool {
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "EOF") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "no route to host") ||
		strings.Contains(msg, "i/o timeout") ||
		strings.Contains(msg, "context deadline exceeded")
}
