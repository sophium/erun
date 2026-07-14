// Package dns01broker authorizes and executes ACME DNS-01 challenge writes on
// behalf of per-env callers, so per-tenant cert issuance is safe on a
// multi-tenant cluster. Its reason to exist is the impersonation guard in this
// file: a caller proven (by its token) to be tenant A's env may only write
// challenge records inside A's own subzone, so A can never prove control of
// tenant B's names even though PowerDNS holds one zone-wide TSIG key centrally.
package dns01broker

import (
	"fmt"
	"strings"
)

// acmeChallengePrefix is the leftmost label every ACME DNS-01 challenge record
// carries. The broker writes nothing else, so requiring it is defense in depth:
// even a valid per-env token cannot use the broker to write an arbitrary record
// (e.g. an A/CNAME) inside its own subzone through the challenge path.
const acmeChallengePrefix = "_acme-challenge."

// AuthorizeChallenge reports nil when the env identified by (tenant, environment)
// may write the ACME DNS-01 challenge record `fqdn` within `servicesZone`, and a
// non-nil error (to surface as 403) otherwise. The env's subzone is
// `<tenant>-<environment>.<servicesZone>`; a challenge record must be an
// `_acme-challenge` name at or below it. Matching is case-insensitive and
// trailing-dot-insensitive (DNS names are both).
//
// The (tenant, environment) come from the verified token, never from the FQDN,
// so a caller cannot widen its own scope by asking for another tenant's name —
// that request simply falls outside its subzone and is refused.
func AuthorizeChallenge(tenant, environment, servicesZone, fqdn string) error {
	tenant = normalizeName(tenant)
	environment = normalizeName(environment)
	servicesZone = normalizeName(servicesZone)
	name := normalizeName(fqdn)
	if tenant == "" || environment == "" || servicesZone == "" {
		return fmt.Errorf("dns01: missing tenant, environment, or services zone")
	}
	if !strings.HasPrefix(name, acmeChallengePrefix) {
		return fmt.Errorf("dns01: %q is not an _acme-challenge record", fqdn)
	}
	// The env's subzone. The tenant is hyphen-free (enforced at registration), so
	// the "<tenant>-<environment>" label is unambiguous and cannot be spoofed by
	// another tenant whose name happens to share a prefix.
	envSuffix := tenant + "-" + environment + "." + servicesZone
	// The wildcard-cert challenge is exactly "_acme-challenge.<subzone>"; a
	// per-host challenge is "_acme-challenge.<host>.<subzone>". Both must sit
	// within the env subzone — the leading dot on the suffix prevents a sibling
	// subzone (e.g. "b-e1.services.z" vs "a-e1.services.z") or an outside zone
	// (e.g. "...services.z.evil.com") from slipping through.
	if name == acmeChallengePrefix+envSuffix || strings.HasSuffix(name, "."+envSuffix) {
		return nil
	}
	return fmt.Errorf("dns01: challenge %q is outside the subzone for tenant %q env %q", fqdn, tenant, environment)
}

func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
