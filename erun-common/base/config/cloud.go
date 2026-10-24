package config

import (
	"sort"
	"strings"
)

const (
	CloudProviderAWS        = "aws"
	CloudProviderCloudflare = "cloudflare"
	CloudProviderERun       = "erun"

	CloudProviderBearerAudience = "erun-api"

	CloudTokenStatusActive        = "active"
	CloudTokenStatusExpired       = "expired"
	CloudTokenStatusNotConfigured = "not_configured"
	CloudTokenStatusUnknown       = "unknown"
)

type CloudProviderConfig struct {
	Alias         string `json:"alias" yaml:"alias"`
	Provider      string `json:"provider" yaml:"provider"`
	Username      string `json:"username,omitempty" yaml:"username,omitempty"`
	AccountID     string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	Profile       string `json:"profile,omitempty" yaml:"profile,omitempty"`
	SSORegion     string `json:"ssoRegion,omitempty" yaml:"ssoregion,omitempty"`
	SSOStartURL   string `json:"ssoStartUrl,omitempty" yaml:"ssostarturl,omitempty"`
	OIDCIssuerURL string `json:"oidcIssuerUrl,omitempty" yaml:"oidcissuerurl,omitempty"`

	// Cloudflare is set only when Provider == CloudProviderCloudflare; the
	// scoped API token is never stored inline, only a reference to the secret
	// store.
	Cloudflare *CloudflareProviderConfig `json:"cloudflare,omitempty" yaml:"cloudflare,omitempty"`

	// ERun is set only when Provider == CloudProviderERun; the refresh token is
	// never stored inline, only a reference to the secret store.
	ERun *ERunProviderConfig `json:"erun,omitempty" yaml:"erun,omitempty"`
}

// CloudflareProviderConfig is the Cloudflare-specific identity for a cloud
// alias. The raw scoped API token never lands in erun-config.yaml; only
// TokenRef, an opaque handle the secret store resolves, is persisted.
type CloudflareProviderConfig struct {
	AccountID string `json:"accountId,omitempty" yaml:"accountid,omitempty"`
	TokenName string `json:"tokenName,omitempty" yaml:"tokenname,omitempty"`
	TokenRef  string `json:"tokenRef,omitempty" yaml:"tokenref,omitempty"`
}

// ParseCloudProviderAlias is the inverse of CloudProviderAlias. The doctor's
// root-config repair flow uses it to reseed setup from a dangling alias when
// the CloudProviderConfig was lost from erun-config but a tenant or
// cloud-context still names it; ok=false means the alias cannot be auto-repaired.
func ParseCloudProviderAlias(alias string) (username, accountID, provider string, ok bool) {
	alias = strings.TrimSpace(alias)
	if alias == "" {
		return "", "", "", false
	}
	at := strings.LastIndex(alias, "@")
	if at <= 0 || at == len(alias)-1 {
		return "", "", "", false
	}
	left := alias[:at]
	provider = strings.ToLower(strings.TrimSpace(alias[at+1:]))
	plus := strings.LastIndex(left, "+")
	if plus <= 0 || plus == len(left)-1 {
		return "", "", "", false
	}
	username = strings.TrimSpace(left[:plus])
	accountID = strings.TrimSpace(left[plus+1:])
	if username == "" || accountID == "" || provider == "" {
		return "", "", "", false
	}
	return username, accountID, provider, true
}

// CloudProviderTypeFromAlias defaults to AWS for any legacy or unparseable
// alias so pre-existing single-alias configs keep resolving to the AWS slot.
func CloudProviderTypeFromAlias(alias string) string {
	if _, _, provider, ok := ParseCloudProviderAlias(alias); ok {
		return provider
	}
	return CloudProviderAWS
}

func NormalizeTenantCloudProviderAliases(aliases []string, primary string) ([]string, string) {
	normalized := NormalizedTenantCloudProviderAliases(aliases)
	primary = strings.TrimSpace(primary)
	if len(normalized) == 0 {
		return nil, ""
	}
	if primary == "" || !cloudAliasListContains(normalized, primary) {
		primary = normalized[0]
	}
	return normalized, primary
}

func NormalizedTenantCloudProviderAliases(aliases []string) []string {
	result := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		if _, ok := seen[alias]; ok {
			continue
		}
		result = append(result, alias)
		seen[alias] = struct{}{}
	}
	sort.Strings(result)
	return result
}

func cloudAliasListContains(aliases []string, alias string) bool {
	for _, candidate := range aliases {
		if candidate == alias {
			return true
		}
	}
	return false
}

type CloudReadStore interface {
	LoadERunConfig() (ERunConfig, string, error)
}
