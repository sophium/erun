package eruncommon

import (
	"fmt"
	"net"
	"strings"
)

// PlatformConfig is the per-instance configuration for an erunpaas platform
// deployment, declared under `platform:` in a project's `.erun/config.yaml`.
//
// The erun-core platform is a generic, installable product: any vendor can
// deploy it under their own names, so the base domain, the delegated services
// zone, the authoritative nameserver, and the platform environment are
// configuration — never hardcoded literals. The values in any one project are
// that deployment's; another vendor's deployment (e.g. on a different base
// domain) supplies its own from the same artifacts.
//
// All fields are optional. An empty block (IsZero) means the project does not
// run a platform deployment. Once any field is set the block is "in use" and
// Validate enforces internal consistency so a malformed block fails fast
// rather than producing inconsistent downstream artifacts.
type PlatformConfig struct {
	// BaseDomain is the registered domain this deployment serves, e.g.
	// "erunpaas.com". Required whenever the block is in use; everything else
	// derives from it.
	BaseDomain string `yaml:"basedomain,omitempty"`
	// Env is the dedicated platform environment that owns the per-deployment
	// global singletons (PowerDNS, the DNS-01 broker), e.g. "frs-prod".
	Env string `yaml:"env,omitempty"`
	// ServicesZone is the child zone delegated to this deployment's PowerDNS,
	// under which tenant services are exposed. Defaults to
	// "services.<BaseDomain>".
	ServicesZone string `yaml:"serviceszone,omitempty"`
	// AuthoritativeIP is the public IP this deployment's authoritative
	// nameserver answers on (the glue-record target for ServicesZone).
	AuthoritativeIP string `yaml:"authoritativeip,omitempty"`
	// Nameservers are the NS hostnames the parent zone delegates ServicesZone
	// to. Defaults to ["ns1.<BaseDomain>", "ns2.<BaseDomain>"].
	Nameservers []string `yaml:"nameservers,omitempty"`
	// AuthHost is the hosted-IdP host, served from the Cloudflare-managed apex
	// zone (not ServicesZone). Defaults to "auth.<BaseDomain>".
	AuthHost string `yaml:"authhost,omitempty"`
	// ACMEEmail is the account email for this deployment's Let's Encrypt
	// registration. LE rate limits are per registered domain, so each
	// deployment uses its own account.
	ACMEEmail string `yaml:"acmeemail,omitempty"`
}

// IsZero reports whether no platform configuration is set, i.e. the project
// does not run a platform deployment.
func (c PlatformConfig) IsZero() bool {
	return strings.TrimSpace(c.BaseDomain) == "" &&
		strings.TrimSpace(c.Env) == "" &&
		strings.TrimSpace(c.ServicesZone) == "" &&
		strings.TrimSpace(c.AuthoritativeIP) == "" &&
		len(c.Nameservers) == 0 &&
		strings.TrimSpace(c.AuthHost) == "" &&
		strings.TrimSpace(c.ACMEEmail) == ""
}

// Resolve returns a copy with the derived defaults filled in from BaseDomain
// (services zone, auth host, nameservers). It does not mutate the receiver and
// does not invent a BaseDomain: with BaseDomain unset the trimmed input is
// returned unchanged and Validate rejects the in-use block.
func (c PlatformConfig) Resolve() PlatformConfig {
	resolved := c
	resolved.BaseDomain = strings.TrimSpace(c.BaseDomain)
	resolved.Env = strings.TrimSpace(c.Env)
	resolved.ServicesZone = strings.TrimSpace(c.ServicesZone)
	resolved.AuthoritativeIP = strings.TrimSpace(c.AuthoritativeIP)
	resolved.AuthHost = strings.TrimSpace(c.AuthHost)
	resolved.ACMEEmail = strings.TrimSpace(c.ACMEEmail)
	if resolved.BaseDomain == "" {
		return resolved
	}
	if resolved.ServicesZone == "" {
		resolved.ServicesZone = "services." + resolved.BaseDomain
	}
	if resolved.AuthHost == "" {
		resolved.AuthHost = "auth." + resolved.BaseDomain
	}
	if len(resolved.Nameservers) == 0 {
		resolved.Nameservers = []string{"ns1." + resolved.BaseDomain, "ns2." + resolved.BaseDomain}
	}
	return resolved
}

// Validate enforces internal consistency of an in-use platform block; it is a
// no-op for an empty block. The rules keep the nothing-hardcoded invariant
// honest: a deployment must declare its own base domain, a services zone under
// that domain, a parseable authoritative IP (when set), an auth host under the
// base domain, and a platform env name that is a clean namespace label.
func (c PlatformConfig) Validate() error {
	if c.IsZero() {
		return nil
	}
	resolved := c.Resolve()
	if resolved.BaseDomain == "" {
		return fmt.Errorf("platform config: basedomain is required when a platform block is set")
	}
	if !isDNSName(resolved.BaseDomain) {
		return fmt.Errorf("platform config: basedomain %q is not a valid domain name", resolved.BaseDomain)
	}
	if !isUnderDomain(resolved.ServicesZone, resolved.BaseDomain) {
		return fmt.Errorf("platform config: serviceszone %q must be %q or a subdomain of it", resolved.ServicesZone, resolved.BaseDomain)
	}
	if !isUnderDomain(resolved.AuthHost, resolved.BaseDomain) {
		return fmt.Errorf("platform config: authhost %q must be %q or a subdomain of it", resolved.AuthHost, resolved.BaseDomain)
	}
	if resolved.AuthoritativeIP != "" && net.ParseIP(resolved.AuthoritativeIP) == nil {
		return fmt.Errorf("platform config: authoritativeip %q is not a valid IP address", resolved.AuthoritativeIP)
	}
	if resolved.Env != "" && normalizeNamespaceName(resolved.Env) != resolved.Env {
		return fmt.Errorf("platform config: env %q must be a DNS-safe namespace label (lowercase letters, digits, and hyphens)", resolved.Env)
	}
	return nil
}

// isUnderDomain reports whether host is base itself or a subdomain of base.
func isUnderDomain(host, base string) bool {
	return host == base || strings.HasSuffix(host, "."+base)
}

// isDNSName reports whether name is a plausible DNS domain name: dotted,
// lowercase, with labels of [a-z0-9-] that do not start or end with a hyphen.
// It is intentionally strict and lowercase-only so config values match the
// charset the rest of erun normalizes hostnames to.
func isDNSName(name string) bool {
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
				continue
			}
			return false
		}
	}
	return true
}
