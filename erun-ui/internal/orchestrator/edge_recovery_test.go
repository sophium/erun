package orchestrator

import "testing"

func TestCutEnvLabelRejectsLabelsTheSweepCouldNeverMatch(t *testing.T) {
	for _, tc := range []struct {
		label       string
		tenant      string
		environment string
		ok          bool
	}{
		{label: "frs/dev", tenant: "frs", environment: "dev", ok: true},
		{label: "no-separator", ok: false},
		{label: "/dev", ok: false},
		{label: "frs/", ok: false},
		{label: "", ok: false},
	} {
		tenant, environment, ok := cutEnvLabel(tc.label)
		if ok != tc.ok || tenant != tc.tenant || environment != tc.environment {
			t.Fatalf("cutEnvLabel(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.label, tenant, environment, ok, tc.tenant, tc.environment, tc.ok)
		}
	}
}
