package eruncommon

import (
	"strings"
	"testing"
)

// A status filter is operator input, and the listing it narrows is the merge
// queue's audit trail: a mistyped value must be refused as a bad argument
// rather than return an empty listing the operator reads as "no gate runs".
func TestNormalizeGateRunStatus(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty means no status filter", "", ""},
		{"blank means no status filter", "   ", ""},
		{"running", "RUNNING", GateRunStatusRunning},
		{"passed", "PASSED", GateRunStatusPassed},
		{"failed", "FAILED", GateRunStatusFailed},
		{"inconclusive", "INCONCLUSIVE", GateRunStatusInconclusive},
		{"lower case resolves to the stored spelling", "failed", GateRunStatusFailed},
		{"surrounding space is ignored", "  passed  ", GateRunStatusPassed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeGateRunStatus(tc.in)
			if err != nil {
				t.Fatalf("NormalizeGateRunStatus(%q) returned an unexpected error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeGateRunStatus(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeGateRunStatusNamesTheAcceptedValues(t *testing.T) {
	for _, in := range []string{"bogus", "SUCCEEDED", "PASS"} {
		got, err := NormalizeGateRunStatus(in)
		if err == nil {
			t.Fatalf("NormalizeGateRunStatus(%q) = %q, want an error naming the accepted values", in, got)
		}
		for _, want := range []string{
			GateRunStatusRunning, GateRunStatusPassed, GateRunStatusFailed, GateRunStatusInconclusive,
		} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("NormalizeGateRunStatus(%q) error %q does not name the accepted value %s", in, err, want)
			}
		}
	}
}

// The refusal must happen before the platform call, so a mistyped filter never
// resolves an alias or reports a real-looking empty result.
func TestRunGateRunListRefusesAnUnknownStatusBeforeAnyPlatformCall(t *testing.T) {
	_, err := RunGateRunList(Context{}, nil, "", GateRunListParams{Status: "bogus"}, CloudDependencies{})
	if err == nil {
		t.Fatalf("RunGateRunList with status %q succeeded, want a bad-argument error", "bogus")
	}
	if !strings.Contains(err.Error(), "unsupported gate run status") {
		t.Fatalf("RunGateRunList error = %q, want the unsupported-status refusal", err)
	}
}
