package eruncommon

import "testing"

func TestServicePortUsesLowerServicePort(t *testing.T) {
	if got := ServicePort(0); got != LowerServicePort {
		t.Fatalf("expected base service port %d, got %d", LowerServicePort, got)
	}
}

func TestMCPServicePortUsesOffsetFromLowerServicePort(t *testing.T) {
	want := LowerServicePort + MCPServicePortOffset
	if MCPServicePort != want {
		t.Fatalf("expected MCP service port %d, got %d", want, MCPServicePort)
	}
}
