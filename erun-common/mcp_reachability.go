package eruncommon

import (
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

// DescribeLocalMCPUnreachable names which of the two failures happened, so the
// reader is not left weighing a busy pod against a dead tunnel. A stale forward
// presents as a timeout with a live listener, which reads exactly like an
// overloaded environment and has cost real debugging time.
func DescribeLocalMCPUnreachable(tenant, environment string, port int) string {
	if LocalPortIsBound(port) {
		return fmt.Sprintf("the port-forward for %s/%s on 127.0.0.1:%d is not carrying traffic (the local port is held but the edge never answers) — re-establishing it", tenant, environment, port)
	}
	return fmt.Sprintf("no port-forward is listening for %s/%s on 127.0.0.1:%d", tenant, environment, port)
}

// DescribeUnrepairedPortForward is the line for the outcome its sibling above
// promises and cannot always deliver: the forward was re-established and the
// edge still answers nothing. Saying so is the point — the alternative is an
// environment that reads as quiet while every client of it is dead, which is
// the failure this whole family is about. The recovery named is a deploy
// because a forward that a fresh `erun open` cannot fix is a runtime problem,
// not a tunnel problem.
func DescribeUnrepairedPortForward(tenant, environment string, port, attempts int) string {
	return fmt.Sprintf(
		"%s/%s is unreachable: its port-forward on 127.0.0.1:%d holds the local port but its edge never answers, and %d attempts to re-establish it did not fix that. Deploy the environment to bring its runtime back.",
		tenant, environment, port, attempts,
	)
}
