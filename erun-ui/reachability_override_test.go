package main

import (
	"net"
	"testing"
)

// The harness's seeded env ports can collide with real listeners on the host,
// so the override must win even when a real listener is bound.
func TestLocalPortReachabilityOverrideWinsOverARealListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind a real listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := tcpListenerPort(t, listener)

	t.Setenv("ERUN_LOCAL_PORT_REACHABILITY_OVERRIDE", "0")
	deps := withDefaultReachabilityDeps(erunUIDeps{})

	if deps.canConnectLocalPort(port) {
		t.Fatalf("canConnectLocalPort(%d) = true with override=0 and a real listener bound; override must win", port)
	}
	if deps.canReachMCPEndpoint(port) {
		t.Fatalf("canReachMCPEndpoint(%d) = true with override=0 and a real listener bound; override must win", port)
	}
}

func TestLocalPortReachabilityOverrideUnsetFallsBackToRealProbe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to bind a real listener: %v", err)
	}
	defer func() { _ = listener.Close() }()
	port := tcpListenerPort(t, listener)

	deps := withDefaultReachabilityDeps(erunUIDeps{})

	if !deps.canConnectLocalPort(port) {
		t.Fatalf("canConnectLocalPort(%d) = false with no override and a real listener bound", port)
	}
}
