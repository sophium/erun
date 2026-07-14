package dns01broker

import "testing"

func TestAuthorizeChallenge(t *testing.T) {
	const zone = "services.example.com"
	cases := []struct {
		name    string
		tenant  string
		env     string
		fqdn    string
		allowed bool
	}{
		// Allowed — the env's own wildcard and per-host challenges, in any casing
		// or with a trailing dot.
		{"own wildcard challenge", "acme", "prod", "_acme-challenge.acme-prod.services.example.com", true},
		{"own per-host challenge", "acme", "prod", "_acme-challenge.mcp.acme-prod.services.example.com", true},
		{"trailing dot + uppercase", "acme", "prod", "_ACME-CHALLENGE.ACME-PROD.SERVICES.EXAMPLE.COM.", true},
		{"env with internal hyphen", "acme", "pr-1", "_acme-challenge.acme-pr-1.services.example.com", true},

		// Rejected — cross-tenant impersonation (the acceptance-criteria negative
		// test): tenant acme cannot write beta's names.
		{"cross-tenant", "acme", "prod", "_acme-challenge.beta-prod.services.example.com", false},
		{"cross-tenant per-host", "acme", "prod", "_acme-challenge.mcp.beta-prod.services.example.com", false},
		// Rejected — a per-env token is scoped to its own env, not the whole tenant.
		{"cross-env same tenant", "acme", "prod", "_acme-challenge.acme-staging.services.example.com", false},
		// Rejected — tenant-prefix confusion: acme must not match acme2.
		{"tenant prefix confusion", "acme", "prod", "_acme-challenge.acme2-prod.services.example.com", false},
		// Rejected — env-prefix confusion: prod must not match prod2.
		{"env prefix confusion", "acme", "prod", "_acme-challenge.acme-prod2.services.example.com", false},
		// Rejected — suffix trick: the subzone appears but a foreign zone follows.
		{"suffix trick outside zone", "acme", "prod", "_acme-challenge.acme-prod.services.example.com.evil.com", false},
		// Rejected — wrong services zone entirely.
		{"wrong services zone", "acme", "prod", "_acme-challenge.acme-prod.services.other.com", false},
		// Rejected — not an ACME challenge record (defense in depth: the broker
		// writes only _acme-challenge names).
		{"not a challenge record", "acme", "prod", "acme-prod.services.example.com", false},
		{"wildcard A name not a challenge", "acme", "prod", "*.acme-prod.services.example.com", false},
		// Rejected — missing inputs.
		{"empty tenant", "", "prod", "_acme-challenge.acme-prod.services.example.com", false},
		{"empty env", "acme", "", "_acme-challenge.acme-prod.services.example.com", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := AuthorizeChallenge(tc.tenant, tc.env, zone, tc.fqdn)
			if tc.allowed && err != nil {
				t.Fatalf("expected allow, got error: %v", err)
			}
			if !tc.allowed && err == nil {
				t.Fatalf("expected reject for %q, got allow", tc.fqdn)
			}
		})
	}
}

// TestAuthorizeChallengeEmptyZone guards the degenerate config where no services
// zone is configured — everything must be refused rather than authorized against
// an empty suffix.
func TestAuthorizeChallengeEmptyZone(t *testing.T) {
	if err := AuthorizeChallenge("acme", "prod", "", "_acme-challenge.acme-prod.services.example.com"); err == nil {
		t.Fatal("expected reject when services zone is empty")
	}
}
