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
	// CAAIssuer, when set, is the CA domain the services zone authorizes via
	// apex CAA records (issue + issuewild), e.g. "letsencrypt.org". Empty means
	// no CAA (any CA may issue). Opt-in because the value must match the CA the
	// cluster edge's ACME server actually uses — a mismatched CAA blocks issuance.
	CAAIssuer string `yaml:"caaissuer,omitempty"`
	// APIURL is this deployment's own API base URL, e.g.
	// "https://api.frs-prod.services.erunpaas.com". Served unauthenticated at
	// GET /v1/platform so a client can discover it. Optional: an empty value
	// renders as an empty string in that response, never an error.
	APIURL string `yaml:"apiurl,omitempty"`
	// ConsoleURL is this deployment's hosted web console URL. Same discovery
	// contract as APIURL.
	ConsoleURL string `yaml:"consoleurl,omitempty"`
	// Brand is this deployment's display name, if the operator set one. Same
	// discovery contract as APIURL.
	Brand string `yaml:"brand,omitempty"`
}

// IsZero reports whether no platform configuration is set, i.e. the project
// does not run a platform deployment.
func (c PlatformConfig) IsZero() bool {
	if len(c.Nameservers) != 0 {
		return false
	}
	for _, field := range []string{
		c.BaseDomain, c.Env, c.ServicesZone, c.AuthoritativeIP,
		c.AuthHost, c.ACMEEmail, c.CAAIssuer, c.APIURL, c.ConsoleURL, c.Brand,
	} {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

// Resolve returns a copy with defaults derived from BaseDomain. It never invents
// a BaseDomain: an unset one is left empty for Validate to reject, not filled in.
func (c PlatformConfig) Resolve() PlatformConfig {
	resolved := c
	resolved.BaseDomain = strings.TrimSpace(c.BaseDomain)
	resolved.Env = strings.TrimSpace(c.Env)
	resolved.ServicesZone = strings.TrimSpace(c.ServicesZone)
	resolved.AuthoritativeIP = strings.TrimSpace(c.AuthoritativeIP)
	resolved.AuthHost = strings.TrimSpace(c.AuthHost)
	resolved.ACMEEmail = strings.TrimSpace(c.ACMEEmail)
	resolved.CAAIssuer = strings.TrimSpace(c.CAAIssuer)
	resolved.APIURL = strings.TrimSpace(c.APIURL)
	resolved.ConsoleURL = strings.TrimSpace(c.ConsoleURL)
	resolved.Brand = strings.TrimSpace(c.Brand)
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

// Validate enforces internal consistency of an in-use platform block and is a
// no-op for an empty one, keeping the nothing-hardcoded invariant honest.
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
	if err := validatePlatformHost("serviceszone", resolved.ServicesZone, resolved.BaseDomain); err != nil {
		return err
	}
	if err := validatePlatformHost("authhost", resolved.AuthHost, resolved.BaseDomain); err != nil {
		return err
	}
	if resolved.AuthoritativeIP != "" && net.ParseIP(resolved.AuthoritativeIP) == nil {
		return fmt.Errorf("platform config: authoritativeip %q is not a valid IP address", resolved.AuthoritativeIP)
	}
	if resolved.Env != "" && normalizeNamespaceName(resolved.Env) != resolved.Env {
		return fmt.Errorf("platform config: env %q must be a DNS-safe namespace label (lowercase letters, digits, and hyphens)", resolved.Env)
	}
	return nil
}

// validatePlatformHost keeps a derived host under the base domain, so the
// nothing-hardcoded invariant holds and the value is safe to pass to helm --set-string.
func validatePlatformHost(field, host, base string) error {
	if !isDNSName(host) {
		return fmt.Errorf("platform config: %s %q is not a valid domain name", field, host)
	}
	if !isUnderDomain(host, base) {
		return fmt.Errorf("platform config: %s %q must be %q or a subdomain of it", field, host, base)
	}
	return nil
}

func isUnderDomain(host, base string) bool {
	return host == base || strings.HasSuffix(host, "."+base)
}

// isDNSName is intentionally strict and lowercase-only so config values match
// the charset the rest of erun normalizes hostnames to.
func isDNSName(name string) bool {
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !isDNSLabel(label) {
			return false
		}
	}
	return true
}

func isDNSLabel(label string) bool {
	if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
		return false
	}
	for _, r := range label {
		if !isDNSLabelChar(r) {
			return false
		}
	}
	return true
}

func isDNSLabelChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-'
}
