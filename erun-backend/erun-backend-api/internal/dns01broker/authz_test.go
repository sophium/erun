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
		{"own wildcard challenge", "acme", "prod", "_acme-challenge.acme-prod.services.example.com", true},
		{"own per-host challenge", "acme", "prod", "_acme-challenge.mcp.acme-prod.services.example.com", true},
		{"trailing dot + uppercase", "acme", "prod", "_ACME-CHALLENGE.ACME-PROD.SERVICES.EXAMPLE.COM.", true},
		{"env with internal hyphen", "acme", "pr-1", "_acme-challenge.acme-pr-1.services.example.com", true},

		{"cross-tenant", "acme", "prod", "_acme-challenge.beta-prod.services.example.com", false},
		{"cross-tenant per-host", "acme", "prod", "_acme-challenge.mcp.beta-prod.services.example.com", false},
		{"cross-env same tenant", "acme", "prod", "_acme-challenge.acme-staging.services.example.com", false},
		{"tenant prefix confusion", "acme", "prod", "_acme-challenge.acme2-prod.services.example.com", false},
		{"env prefix confusion", "acme", "prod", "_acme-challenge.acme-prod2.services.example.com", false},
		{"suffix trick outside zone", "acme", "prod", "_acme-challenge.acme-prod.services.example.com.evil.com", false},
		{"wrong services zone", "acme", "prod", "_acme-challenge.acme-prod.services.other.com", false},
		{"not a challenge record", "acme", "prod", "acme-prod.services.example.com", false},
		{"wildcard A name not a challenge", "acme", "prod", "*.acme-prod.services.example.com", false},
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

func TestAuthorizeChallengeEmptyZone(t *testing.T) {
	if err := AuthorizeChallenge("acme", "prod", "", "_acme-challenge.acme-prod.services.example.com"); err == nil {
		t.Fatal("expected reject when services zone is empty")
	}
}
