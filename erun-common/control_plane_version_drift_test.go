package eruncommon

import (
	"fmt"
	"testing"
)

// control_plane_version_drift_test.go covers one property the integration
// suite cannot reach from the compiled binary (erun-integration/AGENTS.md):
// two configured hostnames that resolve to the *same* backend address must
// be treated as one plane, distinguishable from two hostnames that happen to
// share nothing. The black-box suite has no seam to stub DNS resolution
// across a subprocess boundary, so this exercises groupControlPlanesByBackend
// and detectAdvertisedAPIURLMismatch directly against a fake resolver.

func fakeHostResolver(hostToAddrs map[string][]string) func(Context, string) ([]string, error) {
	return func(_ Context, host string) ([]string, error) {
		addrs, ok := hostToAddrs[host]
		if !ok {
			return nil, fmt.Errorf("no such host %q", host)
		}
		return addrs, nil
	}
}

func erunProvider(alias, apiURL string) CloudProviderStatus {
	return CloudProviderStatus{
		CloudProviderConfig: CloudProviderConfig{
			Alias:    alias,
			Provider: CloudProviderERun,
			ERun:     &ERunProviderConfig{APIURL: apiURL},
		},
	}
}

func TestGroupControlPlanesByBackend(t *testing.T) {
	t.Run("two aliases resolving to the same address are folded into one group", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.example.com":        {"203.0.113.10"},
			"api.vanity.example.com": {"203.0.113.10"},
			"unrelated.example.com":  {"198.51.100.5"},
		})
		planes := []CloudProviderStatus{
			erunProvider("erun+vanity@erun", "https://api.vanity.example.com"),
			erunProvider("erun+real@erun", "https://api.example.com"),
			erunProvider("erun+other@erun", "https://unrelated.example.com"),
		}

		groups := groupControlPlanesByBackend(Context{}, planes, resolve)

		if len(groups) != 2 {
			t.Fatalf("expected 2 groups, got %d: %+v", len(groups), groups)
		}
		merged := groups[0]
		if merged.representative.Alias != "erun+vanity@erun" {
			t.Fatalf("expected the first-seen alias to be the representative, got %q", merged.representative.Alias)
		}
		if len(merged.duplicateAliases) != 1 || merged.duplicateAliases[0] != "erun+real@erun" {
			t.Fatalf("expected erun+real@erun folded in as a duplicate, got %+v", merged.duplicateAliases)
		}
		if groups[1].representative.Alias != "erun+other@erun" {
			t.Fatalf("expected the unrelated alias to stay its own group, got %+v", groups[1])
		}
	})

	t.Run("two aliases resolving to different addresses are never merged", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.one.example.com": {"203.0.113.10"},
			"api.two.example.com": {"203.0.113.20"},
		})
		planes := []CloudProviderStatus{
			erunProvider("erun+one@erun", "https://api.one.example.com"),
			erunProvider("erun+two@erun", "https://api.two.example.com"),
		}

		groups := groupControlPlanesByBackend(Context{}, planes, resolve)

		if len(groups) != 2 {
			t.Fatalf("expected 2 distinct groups, got %d: %+v", len(groups), groups)
		}
		for _, group := range groups {
			if len(group.duplicateAliases) != 0 {
				t.Fatalf("expected no duplicates folded in, got %+v", group)
			}
		}
	})

	t.Run("an alias whose host cannot be resolved is never merged into anything", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.known.example.com": {"203.0.113.10"},
		})
		planes := []CloudProviderStatus{
			erunProvider("erun+known@erun", "https://api.known.example.com"),
			erunProvider("erun+unresolvable@erun", "https://api.unresolvable.example.com"),
		}

		groups := groupControlPlanesByBackend(Context{}, planes, resolve)

		if len(groups) != 2 {
			t.Fatalf("expected the unresolvable alias to remain its own group, got %d: %+v", len(groups), groups)
		}
	})
}

func TestDetectAdvertisedAPIURLMismatch(t *testing.T) {
	t.Run("a discovered apiUrl resolving to a foreign address is flagged", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.other-plane.example.com": {"198.51.100.99"},
		})
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.test.example.com", "https://api.other-plane.example.com", []string{"203.0.113.10:443"}, resolve)
		if reason == "" {
			t.Fatal("expected a mismatch to be flagged")
		}
	})

	t.Run("a discovered apiUrl resolving to the same address as the alias's own host is not flagged", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.vanity.example.com": {"203.0.113.10"},
		})
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.test.example.com", "https://api.vanity.example.com", []string{"203.0.113.10:443"}, resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch for a benign canonical/CNAME-equivalent alias, got %q", reason)
		}
	})

	t.Run("a discovered apiUrl identical to the plane's own is not flagged without resolving anything", func(t *testing.T) {
		resolve := fakeHostResolver(nil) // any lookup call here would fail the test
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.test.example.com", "https://api.test.example.com", []string{"203.0.113.10:443"}, resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch, got %q", reason)
		}
	})

	t.Run("an empty discovered apiUrl is not flagged", func(t *testing.T) {
		resolve := fakeHostResolver(nil)
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.test.example.com", "", []string{"203.0.113.10:443"}, resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch, got %q", reason)
		}
	})

	t.Run("unresolvable own endpoints yields no verdict rather than a guess", func(t *testing.T) {
		resolve := fakeHostResolver(map[string][]string{
			"api.other-plane.example.com": {"198.51.100.99"},
		})
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.test.example.com", "https://api.other-plane.example.com", nil, resolve)
		if reason != "" {
			t.Fatalf("expected no verdict when this plane's own host never resolved, got %q", reason)
		}
	})
}

func TestEndpointsIntersect(t *testing.T) {
	cases := []struct {
		name     string
		a, b     []string
		expected bool
	}{
		{"shared endpoint", []string{"1.2.3.4:443", "5.6.7.8:443"}, []string{"9.9.9.9:443", "5.6.7.8:443"}, true},
		{"disjoint", []string{"1.2.3.4:443"}, []string{"5.6.7.8:443"}, false},
		{"empty either side", nil, []string{"5.6.7.8:443"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := endpointsIntersect(tc.a, tc.b); got != tc.expected {
				t.Fatalf("endpointsIntersect(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.expected)
			}
		})
	}
}
