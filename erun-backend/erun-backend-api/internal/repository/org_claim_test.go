package repository

import "testing"

func TestOrgClaimValue(t *testing.T) {
	cases := []struct {
		name string
		raw  map[string]any
		key  string
		want string
	}{
		{name: "string", raw: map[string]any{"org": "acme"}, key: "org", want: "acme"},
		{name: "trims string", raw: map[string]any{"org": "  acme  "}, key: "org", want: "acme"},
		{name: "integer float renders without decimal", raw: map[string]any{"org": float64(42)}, key: "org", want: "42"},
		{name: "fractional float", raw: map[string]any{"org": 3.5}, key: "org", want: "3.5"},
		{name: "missing key", raw: map[string]any{"other": "x"}, key: "org", want: ""},
		{name: "nil map", raw: nil, key: "org", want: ""},
		{name: "unsupported type", raw: map[string]any{"org": true}, key: "org", want: ""},
		{name: "nested unsupported", raw: map[string]any{"org": map[string]any{"id": "x"}}, key: "org", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := orgClaimValue(tc.raw, tc.key); got != tc.want {
				t.Fatalf("orgClaimValue(%v, %q) = %q, want %q", tc.raw, tc.key, got, tc.want)
			}
		})
	}
}
