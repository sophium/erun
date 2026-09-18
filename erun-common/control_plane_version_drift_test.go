package eruncommon

import (
	"fmt"
	"testing"
)

// control_plane_version_drift_test.go covers the branches of the advertised
// apiUrl check that the integration suite cannot reach from the compiled
// binary (erun-integration/AGENTS.md): telling a benign canonical/CNAME alias
// apart from a foreign backend needs two *resolvable hostnames*, and the
// black-box suite has no seam to stub DNS across a subprocess boundary, so it
// can only drive the literal-address cases. Those live there; the aliasing
// and no-verdict branches are exercised here against a fake resolver.

// fakeHostResolver stands in for CloudDependencies.ResolveHostAddrs, counting
// calls so a case can assert that no lookup was attempted at all.
type fakeHostResolver struct {
	hostToAddrs map[string][]string
	calls       []string
}

func (r *fakeHostResolver) resolve(_ Context, host string) ([]string, error) {
	r.calls = append(r.calls, host)
	addrs, ok := r.hostToAddrs[host]
	if !ok {
		return nil, fmt.Errorf("no such host %q", host)
	}
	return addrs, nil
}

func TestDetectAdvertisedAPIURLMismatch(t *testing.T) {
	t.Run("a discovered apiUrl resolving to the same address as this plane's own host is not flagged", func(t *testing.T) {
		// A vanity hostname CNAMEing to the one erun queried: different name,
		// same backend, so the textual difference alone is not evidence.
		resolve := &fakeHostResolver{hostToAddrs: map[string][]string{
			"api.vanity.example.com": {"203.0.113.10"},
			"api.real.example.com":   {"203.0.113.10"},
		}}
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.real.example.com", "https://api.vanity.example.com", resolve.resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch for a benign canonical alias, got %q", reason)
		}
	})

	t.Run("an unresolvable discovered host yields no verdict rather than a guess", func(t *testing.T) {
		resolve := &fakeHostResolver{hostToAddrs: map[string][]string{
			"api.real.example.com": {"203.0.113.10"},
		}}
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.real.example.com", "https://api.other-plane.example.com", resolve.resolve)
		if reason != "" {
			t.Fatalf("expected no verdict when the discovered host does not resolve, got %q", reason)
		}
	})

	t.Run("an unresolvable own host yields no verdict rather than a guess", func(t *testing.T) {
		resolve := &fakeHostResolver{hostToAddrs: map[string][]string{
			"api.other-plane.example.com": {"198.51.100.99"},
		}}
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.real.example.com", "https://api.other-plane.example.com", resolve.resolve)
		if reason != "" {
			t.Fatalf("expected no verdict when this plane's own host does not resolve, got %q", reason)
		}
	})

	t.Run("a discovered apiUrl identical to the plane's own is not flagged without resolving anything", func(t *testing.T) {
		resolve := &fakeHostResolver{}
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.real.example.com", "https://api.real.example.com/", resolve.resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch, got %q", reason)
		}
		if len(resolve.calls) != 0 {
			t.Fatalf("expected no lookup for an identical apiUrl, got %v", resolve.calls)
		}
	})

	t.Run("an empty discovered apiUrl is not flagged", func(t *testing.T) {
		resolve := &fakeHostResolver{}
		reason := detectAdvertisedAPIURLMismatch(Context{}, "erun+test@erun", "https://api.real.example.com", "", resolve.resolve)
		if reason != "" {
			t.Fatalf("expected no mismatch, got %q", reason)
		}
		if len(resolve.calls) != 0 {
			t.Fatalf("expected no lookup when the plane reported no apiUrl, got %v", resolve.calls)
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
