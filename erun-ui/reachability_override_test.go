package main

import (
	"net"
	"testing"
)

// TestLocalPortReachabilityOverrideWinsOverARealListener pins the invariant
// that the headless Playwright harness's isolated config store depends on:
// it computes local port ranges purely from its own seeded tenant/env
// list, so a seeded env's port can coincide with a real port a genuine
// environment on the same host has bound (this host's own MCP/SSH forwards,
// or another agent's). Unforced, canReachMCPEndpoint/canConnectLocalPort read
// that real listener as if it belonged to the seeded (never-deployed) env.
// ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE must win even when a real listener is
// present on the exact probed port — the adverse condition that used to leak.
func TestLocalPortReachabilityOverrideWinsOverARealListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind a real listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port

	t.Setenv("ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE", "0")
	deps := withDefaultReachabilityDeps(erunUIDeps{})

	if deps.canConnectLocalPort(port) {
		t.Fatalf("canConnectLocalPort(%d) = true with override=0 and a real listener bound; override must win", port)
	}
	if deps.canReachMCPEndpoint(port) {
		t.Fatalf("canReachMCPEndpoint(%d) = true with override=0 and a real listener bound; override must win", port)
	}
}

// TestLocalPortReachabilityOverrideUnsetFallsBackToRealProbe pins that the
// override is opt-in: with no env var set, the real listener above is
// observed as reachable so production behavior (a genuine stale-forward vs.
// no-forward diagnosis) is unchanged.
func TestLocalPortReachabilityOverrideUnsetFallsBackToRealProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind a real listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := listener.Addr().(*net.TCPAddr).Port

	deps := withDefaultReachabilityDeps(erunUIDeps{})

	if !deps.canConnectLocalPort(port) {
		t.Fatalf("canConnectLocalPort(%d) = false with no override and a real listener bound", port)
	}
}
