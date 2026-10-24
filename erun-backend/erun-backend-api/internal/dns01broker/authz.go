// Package dns01broker authorizes and executes ACME DNS-01 challenge writes so a
// per-env token can only write challenges inside its own tenant's subzone.
package dns01broker

import (
	"fmt"
	"strings"
)

// acmeChallengePrefix keeps even a valid token from writing non-challenge records.
const acmeChallengePrefix = "_acme-challenge."

// AuthorizeChallenge reports whether the env may write challenge record fqdn.
// tenant and environment come from the verified token, never from fqdn.
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
	// Tenants are hyphen-free (enforced at registration), so this label is unambiguous.
	envSuffix := tenant + "-" + environment + "." + servicesZone
	// The leading dot keeps sibling subzones and outside zones from matching.
	if name == acmeChallengePrefix+envSuffix || strings.HasSuffix(name, "."+envSuffix) {
		return nil
	}
	return fmt.Errorf("dns01: challenge %q is outside the subzone for tenant %q env %q", fqdn, tenant, environment)
}

func normalizeName(name string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
}
