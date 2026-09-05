package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"

	eruncommon "github.com/sophium/erun/erun-common"
)

// errMCPUnreachable signals the frontend to render an unreachable state with a
// Reconnect action. Side-effecting recovery — which can redeploy the runtime —
// is gated on that explicit user action; MCP readers never recover implicitly.
var errMCPUnreachable = errors.New("mcp unreachable")

// mcpUnreachableMarker is prefixed onto every surfaced error message so the
// frontend can detect the unreachable state without depending on locale or
// underlying error text. The marker is opaque; it is never shown to users.
const mcpUnreachableMarker = "ERUN_MCP_UNREACHABLE: "

// mcpUnreachableKindMarkers give the review panel the reachability kind
// DescribeLocalMCPUnreachable already computed, rather than asking it to
// pattern-match the sentence (#1230): a never-opened/stopped environment reads
// as informational, a stale forward reads as a fault, and the frontend cannot
// tell those apart from prose alone. Both are still recognised as "MCP
// unreachable" the same way the plain marker is.
var mcpUnreachableKindMarkers = map[eruncommon.LocalMCPUnreachableKind]string{
	eruncommon.LocalMCPNotOpen:      "ERUN_MCP_UNREACHABLE_NOT_OPEN: ",
	eruncommon.LocalMCPStaleForward: "ERUN_MCP_UNREACHABLE_STALE: ",
}

func wrapMCPUnreachableError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s%w: %w", mcpUnreachableMarker, errMCPUnreachable, err)
}

// wrapMCPUnreachableErrorWithKind is wrapMCPUnreachableError plus the
// reachability kind the caller already classified, so the review panel can
// tell a stopped environment (informational, "Open") from a stale forward (a
// fault, "Reconnect…") without re-deriving it from the message text.
func wrapMCPUnreachableErrorWithKind(kind eruncommon.LocalMCPUnreachableKind, err error) error {
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
