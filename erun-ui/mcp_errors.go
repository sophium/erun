package main

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
)

// errMCPUnreachable signals the frontend to render an unreachable state with a
// Reconnect action. Side-effecting recovery — which can redeploy the runtime —
// is gated on that explicit user action; MCP readers never recover implicitly.
var errMCPUnreachable = errors.New("mcp unreachable")

// mcpUnreachableMarker is prefixed onto every surfaced error message so the
// frontend can detect the unreachable state without depending on locale or
// underlying error text. The marker is opaque; it is never shown to users.
const mcpUnreachableMarker = "ERUN_MCP_UNREACHABLE: "

func wrapMCPUnreachableError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s%w: %w", mcpUnreachableMarker, errMCPUnreachable, err)
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
