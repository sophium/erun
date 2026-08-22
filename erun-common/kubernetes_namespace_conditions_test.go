package eruncommon

import (
	"strings"
	"testing"
)

// TestFormatNamespaceConditionsMatchesTheOperatorDiagnosis pins #1140's
// example verbatim: a namespace stuck on a Challenge finalizer must read as
// an actionable diagnosis, not a bare timeout.
func TestFormatNamespaceConditionsMatchesTheOperatorDiagnosis(t *testing.T) {
	raw := "NamespaceContentRemaining=True\tchallenges.acme.cert-manager.io has 1 resource instances\n" +
		"NamespaceFinalizersRemaining=True\tacme.cert-manager.io/finalizer in 1 resource instances\n"

	got := formatNamespaceConditions(raw)

	want := "NamespaceContentRemaining=True     challenges.acme.cert-manager.io has 1 resource instances\n" +
		"NamespaceFinalizersRemaining=True  acme.cert-manager.io/finalizer in 1 resource instances"
	if got != want {
		t.Fatalf("formatNamespaceConditions() =\n%s\nwant\n%s", got, want)
	}
}

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
	err := &NamespaceTerminationBlockedError{Namespace: "acme-probej", Detail: "Foo=True  bar"}
	got := err.Error()
	if got == "" || !strings.Contains(got, "acme-probej") || !strings.Contains(got, "Foo=True  bar") {
		t.Fatalf("Error() = %q, want it to name the namespace and carry the detail", got)
	}

	bare := &NamespaceTerminationBlockedError{Namespace: "acme-probej"}
	if got := bare.Error(); !strings.Contains(got, "acme-probej") {
		t.Fatalf("Error() = %q, want it to name the namespace even with no detail", got)
	}
}
