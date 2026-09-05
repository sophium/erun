package eruncommon

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"
)

// localMCPProbeTimeout bounds the reachability probe. This is a loopback round
// trip to a port-forward, so anything slower than this is the tunnel failing
// rather than the edge thinking, and waiting longer only delays the recovery.
const localMCPProbeTimeout = 1500 * time.Millisecond

// localPortDialTimeout bounds the "is anything holding this port" check, which
// only completes a TCP handshake against loopback.
const localPortDialTimeout = 200 * time.Millisecond

// mcpStartupReachabilityWait bounds awaitLocalMCPEndpointReachable: long
// enough to cover the ordinary gap between an MCP client session starting and
// `erun open` finishing the port-forward it depends on, short enough that a
// genuinely unopened environment still fails within one request rather than
// hanging the client's handshake indefinitely.
const mcpStartupReachabilityWait = 20 * time.Second

// mcpStartupReachabilityPoll is how often awaitLocalMCPEndpointReachable
// re-probes while waiting.
const mcpStartupReachabilityPoll = 500 * time.Millisecond

// awaitLocalMCPEndpointReachable blocks until the local port answers, wait
// elapses, or ctx ends -- whichever comes first. It never returns an error and
// does not report which of those happened: the caller's own dial (or its own
// already-bound-but-dead check) is what actually decides the request's
// outcome, so a still-unreachable port after this wait simply proceeds to
// fail exactly as it would have without the wait, only later.
func awaitLocalMCPEndpointReachable(ctx context.Context, port int, wait time.Duration) {
	if port <= 0 || CanReachLocalMCPEndpoint(port) {
		return
	}
	deadline := time.Now().Add(wait)
	ticker := time.NewTicker(mcpStartupReachabilityPoll)
	defer ticker.Stop()
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if CanReachLocalMCPEndpoint(port) {
				return
			}
		}
	}
}

// LocalMCPEndpoint is the loopback URL a port-forward exposes an environment's
// MCP edge on.
func LocalMCPEndpoint(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

// CanReachLocalMCPEndpoint reports whether the local MCP port actually carries
// traffic to the edge, by making a real round trip.
//
// A TCP dial is not sufficient, and that insufficiency is the whole reason this
// exists: a stale kubectl port-forward keeps accepting connections and then
// never answers, so a dial-based check reports a healthy tunnel while every call
// through it hangs. Any HTTP response is success — an unauthenticated request is
// answered 401, and a 401 already proves the edge replied.
func CanReachLocalMCPEndpoint(port int) bool {
	if port <= 0 {
		return false
	}
	client := http.Client{Timeout: localMCPProbeTimeout}
	resp, err := client.Get(LocalMCPEndpoint(port))
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// LocalPortIsBound reports whether something holds the port. Paired with
// CanReachLocalMCPEndpoint it separates "there is no forward" from "there is a
// forward and it has gone stale" — the same symptom from the caller's side, but
// not the same problem, and worth saying apart.
func LocalPortIsBound(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), localPortDialTimeout)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// LocalMCPUnreachableKind names which of the two locally observable failure
// shapes explains why an environment's MCP edge cannot be reached, so a caller
// can act on the distinction directly instead of re-deriving it by
// pattern-matching DescribeLocalMCPUnreachable's prose (#1230).
type LocalMCPUnreachableKind string

const (
	// LocalMCPNotOpen means nothing holds the local port — the ordinary shape
	// of an environment nobody has opened, or one that was stopped.
	LocalMCPNotOpen LocalMCPUnreachableKind = "not-open"
	// LocalMCPStaleForward means the local port is held but the edge behind it
	// never answers — a forward that needs re-establishing, not starting.
	LocalMCPStaleForward LocalMCPUnreachableKind = "stale-forward"
)

// ClassifyLocalMCPUnreachable reports which of the two locally observable
// failure shapes applies for a port that CanReachLocalMCPEndpoint has already
// reported unreachable.
func ClassifyLocalMCPUnreachable(port int) LocalMCPUnreachableKind {
	if LocalPortIsBound(port) {
		return LocalMCPStaleForward
	}
	return LocalMCPNotOpen
}

// DescribeLocalMCPUnreachable names which of the two failures happened, so the
// reader is not left weighing a busy pod against a dead tunnel. A stale forward
// presents as a timeout with a live listener, which reads exactly like an
// overloaded environment and has cost real debugging time.
func DescribeLocalMCPUnreachable(tenant, environment string, port int) string {
	if ClassifyLocalMCPUnreachable(port) == LocalMCPStaleForward {
		return fmt.Sprintf("the port-forward for %s/%s on 127.0.0.1:%d is not carrying traffic (the local port is held but the edge never answers) — re-establishing it", tenant, environment, port)
	}
	return fmt.Sprintf("no port-forward is listening for %s/%s on 127.0.0.1:%d", tenant, environment, port)
}

// DescribeUnrepairedPortForward is the line for the outcome its sibling above
// promises and cannot always deliver: the forward was re-established and the
// environment is still unreachable. Saying so is the point — the alternative is
// an environment that reads as quiet while every client of it is dead, which is
// the failure this whole family is about. The recovery named is a deploy
// because a forward that a fresh `erun open` cannot fix is a runtime problem,
// not a tunnel problem.
//
// The health decides how the fault is described, because the two shapes read as
// opposite things to whoever finds the port: one is held by something that
// answers nothing, the other is not held at all.
func DescribeUnrepairedPortForward(tenant, environment string, port, attempts int, health PortForwardHealth) string {
	return fmt.Sprintf(
		"%s/%s is unreachable: %s, and %d attempts to re-establish it did not fix that. Deploy the environment to bring its runtime back.",
		tenant, environment, describePortForwardFault(port, health), attempts,
	)
}

func describePortForwardFault(port int, health PortForwardHealth) string {
	if health == PortForwardDropped {
		return fmt.Sprintf("its port-forward on 127.0.0.1:%d is gone and nothing holds the port", port)
	}
	return fmt.Sprintf("its port-forward on 127.0.0.1:%d holds the local port but its edge never answers", port)
}
