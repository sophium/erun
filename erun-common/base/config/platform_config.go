package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// PlatformConfig is a platform deployment's `platform:` block. Every vendor-specific
// name is configuration; an empty block means no platform deployment.
type PlatformConfig struct {
	// BaseDomain is the deployment's registered domain; everything else derives from it.
	BaseDomain string `yaml:"basedomain,omitempty"`
	// Env is the platform environment owning global singletons (PowerDNS, DNS-01 broker).
	Env string `yaml:"env,omitempty"`
	// ServicesZone is the child zone delegated to PowerDNS; defaults to "services.<BaseDomain>".
	ServicesZone string `yaml:"serviceszone,omitempty"`
	// AuthoritativeIP is the nameserver's public IP (the glue-record target).
	AuthoritativeIP string `yaml:"authoritativeip,omitempty"`
	// Nameservers default to ["ns1.<BaseDomain>", "ns2.<BaseDomain>"].
	Nameservers []string `yaml:"nameservers,omitempty"`
	// AuthHost is the hosted-IdP host on the apex zone; defaults to "auth.<BaseDomain>".
	AuthHost string `yaml:"authhost,omitempty"`
	// ACMEEmail is per deployment because LE rate limits are per registered domain.
	ACMEEmail string `yaml:"acmeemail,omitempty"`
	// CAAIssuer opts into CAA records; it must match the edge's ACME CA or issuance fails.
	CAAIssuer string `yaml:"caaissuer,omitempty"`
	// APIURL is served unauthenticated at GET /v1/platform for discovery; may be empty.
	APIURL     string `yaml:"apiurl,omitempty"`
	ConsoleURL string `yaml:"consoleurl,omitempty"`
	Brand      string `yaml:"brand,omitempty"`
	// DocsURL defaults to "https://docs.<BaseDomain>".
	DocsURL string `yaml:"docsurl,omitempty"`
	Tagline string `yaml:"tagline,omitempty"`
	// LogoURL is absolute because one console image serves every instance.
	LogoURL string `yaml:"logourl,omitempty"`
}

func (c PlatformConfig) IsZero() bool {
	if len(c.Nameservers) != 0 {
		return false
	}
	for _, field := range []string{
		c.BaseDomain, c.Env, c.ServicesZone, c.AuthoritativeIP,
		c.AuthHost, c.ACMEEmail, c.CAAIssuer, c.APIURL, c.ConsoleURL, c.Brand,
		c.DocsURL, c.Tagline, c.LogoURL,
	} {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

// Resolve fills defaults from BaseDomain; it never invents a BaseDomain.
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
	// A trailing slash would double up in docs deep links.
	resolved.DocsURL = strings.TrimRight(strings.TrimSpace(c.DocsURL), "/")
	resolved.Tagline = strings.TrimSpace(c.Tagline)
	resolved.LogoURL = strings.TrimSpace(c.LogoURL)
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
	if resolved.DocsURL == "" {
		resolved.DocsURL = "https://docs." + resolved.BaseDomain
	}
	return resolved
}

// Validate checks an in-use block and is a no-op for an empty one.
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
	if err := validatePlatformAddresses(resolved); err != nil {
		return err
	}
	if resolved.AuthoritativeIP != "" && net.ParseIP(resolved.AuthoritativeIP) == nil {
		return fmt.Errorf("platform config: authoritativeip %q is not a valid IP address", resolved.AuthoritativeIP)
	}
	if resolved.Env != "" && NormalizeNamespaceName(resolved.Env) != resolved.Env {
		return fmt.Errorf("platform config: env %q must be a DNS-safe namespace label (lowercase letters, digits, and hyphens)", resolved.Env)
	}
	return nil
}

// validatePlatformAddresses keeps derived hosts under the base domain and
// discovery URLs absolute.
func validatePlatformAddresses(resolved PlatformConfig) error {
	if err := validatePlatformHost("serviceszone", resolved.ServicesZone, resolved.BaseDomain); err != nil {
		return err
	}
	if err := validatePlatformHost("authhost", resolved.AuthHost, resolved.BaseDomain); err != nil {
		return err
	}
	if err := validatePlatformURL("docsurl", resolved.DocsURL, "https://docs."+resolved.BaseDomain); err != nil {
		return err
	}
	return validatePlatformURL("logourl", resolved.LogoURL, "https://"+resolved.BaseDomain+"/logo.svg")
}

// validatePlatformURL requires an absolute http(s) URL, since clients render it verbatim.
func validatePlatformURL(field, value, example string) error {
	if value == "" {
		return nil
	}
	parsed, err := url.Parse(value)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("platform config: %s %q must be an absolute URL including the scheme and host, for example %q", field, value, example)
	}
	return nil
}

// validatePlatformHost keeps a derived host under the base domain, which also
// makes it safe for helm --set-string.
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

// isDNSName is strict and lowercase-only to match erun's hostname normalization.
func isDNSName(name string) bool {
	if name == "" || len(name) > 253 || !strings.Contains(name, ".") {
		return false
	}
	for _, label := range strings.Split(name, ".") {
		if !IsDNSLabel(label) {
			return false
		}
	}
	return true
}

func IsDNSLabel(label string) bool {
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
