package main

import (
	"testing"

	"github.com/sophium/erun/erun-ui/internal/contribute"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// The contribute service names its tab kinds by value; the terminal's idle,
// AI-status and reconnect handling match on the terminal kinds.
func TestContributeSessionKindsAreTheTerminals(t *testing.T) {
	kinds := map[string]terminal.Kind{
		contribute.KindERun: terminal.KindContributeERun,
		contribute.KindAI:   terminal.KindContributeAI,
	}
	for name, kind := range kinds {
		if name != string(kind) {
			t.Fatalf("contribute kind %q does not match terminal kind %q", name, kind)
		}
	}
}
