package eruncommon

import "testing"

func TestResolvedRuntimeChartName(t *testing.T) {
	cases := []struct {
		name            string
		tenant          string
		tenantPublished bool
		want            string
	}{
		{"tenant chart published", "frs", true, "frs-devops"},
		{"tenant chart not published falls back", "frs", false, "erun-devops"},
		{"erun product tenant always canonical", "", true, "erun-devops"},
		{"erun product tenant, unpublished flag ignored", "", false, "erun-devops"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ResolvedRuntimeChartName(tc.tenant, tc.tenantPublished); got != tc.want {
				t.Fatalf("ResolvedRuntimeChartName(%q, %v) = %q, want %q", tc.tenant, tc.tenantPublished, got, tc.want)
			}
		})
	}
}
