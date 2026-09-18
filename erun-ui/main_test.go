package main

import (
	"strings"
	"testing"
)

// TestWantsUsage pins the flags that must short-circuit main before any side
// effect. A help probe that reaches the launch path starts a whole desktop —
// workspace syncs, orchestrator pacing, and a control record written over a
// running instance's — which is how a "what flags does this take?" question
// stranded a live desktop's restart endpoint.
func TestWantsUsage(t *testing.T) {
	for _, arg := range []string{"-h", "--help", "-help"} {
		if !wantsUsage([]string{arg}) {
			t.Fatalf("wantsUsage(%q) = false, want true", arg)
		}
		// Position must not matter: the binary is launched with flags the
		// parser recognizes in any order.
		if !wantsUsage([]string{"--port", "34123", arg}) {
			t.Fatalf("wantsUsage with %q after other flags = false, want true", arg)
		}
	}
	for _, args := range [][]string{
		{},
		{"--headless"},
		{"--headless", "--port", "34123"},
		{"--port=34123"},
		{"help"},
	} {
		if wantsUsage(args) {
			t.Fatalf("wantsUsage(%v) = true, want false", args)
		}
	}
}

// TestPrintAppUsageDocumentsTheRealFlags keeps the usage text honest: it is
// the only thing the binary prints for a help probe, so a flag it does not
// name is a flag an operator cannot discover.
func TestPrintAppUsageDocumentsTheRealFlags(t *testing.T) {
	var printed strings.Builder
	printAppUsage(&printed)
	for _, want := range []string{"--headless", "--port", "--help"} {
		if !strings.Contains(printed.String(), want) {
			t.Fatalf("usage does not document %s:\n%s", want, printed.String())
		}
	}
}
