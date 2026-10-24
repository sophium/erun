package kube

import (
	"strings"
	"testing"
)

func TestFormatNamespaceConditionsEmptyInput(t *testing.T) {
	if got := formatNamespaceConditions(""); got != "" {
		t.Fatalf("formatNamespaceConditions(\"\") = %q, want empty", got)
	}
	if got := formatNamespaceConditions("\n\n"); got != "" {
		t.Fatalf("formatNamespaceConditions(blank lines) = %q, want empty", got)
	}
}

func TestFormatNamespaceConditionsSingleConditionNoPadding(t *testing.T) {
	got := formatNamespaceConditions("NamespaceDeletionDiscoveryFailure=True\tsome message\n")
	want := "NamespaceDeletionDiscoveryFailure=True  some message"
	if got != want {
		t.Fatalf("formatNamespaceConditions() = %q, want %q", got, want)
	}
}

func TestNamespaceTerminationBlockedErrorMessage(t *testing.T) {
	bare := &NamespaceTerminationBlockedError{Namespace: "acme-probej"}
	if got := bare.Error(); !strings.Contains(got, "acme-probej") {
		t.Fatalf("Error() = %q, want it to name the namespace even with no detail", got)
	}
}
