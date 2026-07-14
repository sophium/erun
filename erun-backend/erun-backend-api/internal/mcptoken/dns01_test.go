package mcptoken

import (
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

func newTestSigner(t *testing.T) *Signer {
	t.Helper()
	privatePEM, _, err := eruncommon.GenerateDesktopIdentity()
	if err != nil {
		t.Fatalf("generate identity: %v", err)
	}
	signer, err := NewSigner(privatePEM)
	if err != nil {
		t.Fatalf("new signer: %v", err)
	}
	return signer
}

// TestSignDNS01RoundTrip proves a minted DNS-01 token self-verifies back to the
// (tenant, environment) it was minted for, with a long lifetime.
func TestSignDNS01RoundTrip(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	token, audience, err := signer.SignDNS01("acme", "prod", now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if want := "erun-dns01:acme/prod"; audience != want {
		t.Fatalf("audience = %q, want %q", audience, want)
	}
	tenant, environment, err := signer.VerifyDNS01(token, now)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if tenant != "acme" || environment != "prod" {
		t.Fatalf("verify = (%q,%q), want (acme,prod)", tenant, environment)
	}
	// Long-lived so it survives cert renewals; still valid well beyond an hour.
	if _, _, err := signer.VerifyDNS01(token, now.Add(48*time.Hour)); err != nil {
		t.Fatalf("token should still be valid after 48h: %v", err)
	}
}

func TestVerifyDNS01RejectsExpired(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	token, _, err := signer.SignDNS01("acme", "prod", now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, err := signer.VerifyDNS01(token, now.Add(2*dns01TokenTTL)); err == nil {
		t.Fatal("expected an expired-token error")
	}
}

// TestVerifyDNS01RejectsForeignKey proves a token minted by one backend key is
// rejected by another — the signature check is real.
func TestVerifyDNS01RejectsForeignKey(t *testing.T) {
	minter := newTestSigner(t)
	verifier := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	token, _, err := minter.SignDNS01("acme", "prod", now)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if _, _, err := verifier.VerifyDNS01(token, now); err == nil {
		t.Fatal("expected a signature error for a token signed by a different key")
	}
}

// TestVerifyDNS01RejectsMCPToken proves cross-capability replay is blocked: an
// MCP-audience token (same signing key) is not a valid DNS-01 token, so a leaked
// MCP token cannot drive DNS writes.
func TestVerifyDNS01RejectsMCPToken(t *testing.T) {
	signer := newTestSigner(t)
	now := time.Unix(1_700_000_000, 0)
	mcpToken, _, err := signer.Sign("acme", "prod", "user-1", now)
	if err != nil {
		t.Fatalf("sign mcp: %v", err)
	}
	if _, _, err := signer.VerifyDNS01(mcpToken, now); err == nil {
		t.Fatal("expected an audience error: an MCP token must not verify as a DNS-01 token")
	}
}

func TestParseDNS01Audience(t *testing.T) {
	cases := map[string]struct {
		tenant, env string
		ok          bool
	}{
		"erun-dns01:acme/prod": {"acme", "prod", true},
		"erun-dns01:acme/pr-1": {"acme", "pr-1", true},
		"erun-mcp:acme/prod":   {"", "", false},
		"erun-dns01:acme":      {"", "", false},
		"erun-dns01:/prod":     {"", "", false},
		"erun-dns01:acme/":     {"", "", false},
		"":                     {"", "", false},
	}
	for audience, want := range cases {
		t.Run(audience, func(t *testing.T) {
			tenant, env, ok := ParseDNS01Audience(audience)
			if ok != want.ok || tenant != want.tenant || env != want.env {
				t.Fatalf("ParseDNS01Audience(%q) = (%q,%q,%v), want (%q,%q,%v)", audience, tenant, env, ok, want.tenant, want.env, want.ok)
			}
		})
	}
}
