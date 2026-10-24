package mcp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/sophium/erun/erun-common/infra/kube"
)

// A loopback round trip slower than this is a failing tunnel, not a busy edge.
const localMCPProbeTimeout = 1500 * time.Millisecond

const localPortDialTimeout = 200 * time.Millisecond

// mcpStartupReachabilityWait covers the gap until `erun open` finishes the forward, while an
// unopened environment still fails within one request.
const mcpStartupReachabilityWait = 20 * time.Second

const mcpStartupReachabilityPoll = 500 * time.Millisecond

// awaitLocalMCPEndpointReachable reports nothing; the caller's own request decides the outcome.
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

// LocalMCPEndpoint is the loopback URL of a port-forwarded MCP edge.
func LocalMCPEndpoint(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/mcp", port)
}

// CanReachLocalMCPEndpoint makes a real round trip, since a stale port-forward accepts
// connections and never answers. Any HTTP response, including 401, is success.
func CanReachLocalMCPEndpoint(port int) bool {
	return probeLocalMCPEndpoint(port) == localMCPAnswered
}

// localMCPProbe keeps the fault kind a boolean would lose.
type localMCPProbe int

const (
	localMCPAnswered localMCPProbe = iota
	// localMCPRefused: nothing holds the port.
	localMCPRefused
	// localMCPDropped: accepted then closed without a reply, as when the target is still coming up.
	localMCPDropped
	// localMCPSilent: accepted and never answered, as a wedged forward does.
	localMCPSilent
)

// probeLocalMCPEndpoint tells refused from dropped by whether the port is held; HTTP reports both alike.
func probeLocalMCPEndpoint(port int) localMCPProbe {
	if port <= 0 {
		return localMCPRefused
	}
	client := http.Client{Timeout: localMCPProbeTimeout}
	resp, err := client.Get(LocalMCPEndpoint(port))
	if err == nil {
		_ = resp.Body.Close()
		return localMCPAnswered
	}
	if !LocalPortIsBound(port) {
		return localMCPRefused
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return localMCPSilent
	}
	return localMCPDropped
}

// LocalPortIsBound reports whether something holds the port.
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

// LocalMCPUnreachableKind names why the local MCP edge is unreachable.
type LocalMCPUnreachableKind string

const (
	// LocalMCPNotOpen means nothing holds the local port.
	LocalMCPNotOpen LocalMCPUnreachableKind = "not-open"
	// LocalMCPStaleForward means the port accepts but never answers; re-establish the forward.
	LocalMCPStaleForward LocalMCPUnreachableKind = "stale-forward"
	// LocalMCPTargetNotAnswering means the forward is up but the target closed without replying;
	// wait rather than re-open.
	LocalMCPTargetNotAnswering LocalMCPUnreachableKind = "target-not-answering"
)

// ClassifyLocalMCPUnreachable assumes CanReachLocalMCPEndpoint already reported the port unreachable.
func ClassifyLocalMCPUnreachable(port int) LocalMCPUnreachableKind {
	switch probeLocalMCPEndpoint(port) {
	case localMCPDropped:
		return LocalMCPTargetNotAnswering
	case localMCPSilent:
		return LocalMCPStaleForward
	case localMCPAnswered, localMCPRefused:
		return LocalMCPNotOpen
	}
	return LocalMCPNotOpen
}

// DescribeLocalMCPUnreachable names the failure; the held-port shapes call for opposite first moves.
func DescribeLocalMCPUnreachable(tenant, environment string, port int) string {
	switch ClassifyLocalMCPUnreachable(port) {
	case LocalMCPStaleForward:
		return fmt.Sprintf("the port-forward for %s/%s on 127.0.0.1:%d is not carrying traffic (the local port is held but the edge never answers) — re-establishing it", tenant, environment, port)
	case LocalMCPTargetNotAnswering:
		return fmt.Sprintf("the port-forward for %s/%s on 127.0.0.1:%d is up but the environment behind it did not answer (the local port took the connection and closed it without a reply) — the environment is most likely still starting, so retry shortly, and re-establish the forward only if it stays unresponsive", tenant, environment, port)
	case LocalMCPNotOpen:
	}
	return fmt.Sprintf("no port-forward is listening for %s/%s on 127.0.0.1:%d", tenant, environment, port)
}

// DescribeUnrepairedPortForward reports a forward still unreachable after re-establishing. It
// names a deploy, since `erun open` could not fix it.
func DescribeUnrepairedPortForward(tenant, environment string, port, attempts int, health kube.PortForwardHealth) string {
	return fmt.Sprintf(
		"%s/%s is unreachable: %s, and %d attempts to re-establish it did not fix that. Deploy the environment to bring its runtime back.",
		tenant, environment, describePortForwardFault(port, health), attempts,
	)
}

func describePortForwardFault(port int, health kube.PortForwardHealth) string {
	if health == kube.PortForwardDropped {
		return fmt.Sprintf("its port-forward on 127.0.0.1:%d is gone and nothing holds the port", port)
	}
	return fmt.Sprintf("its port-forward on 127.0.0.1:%d holds the local port but its edge never answers", port)
}
