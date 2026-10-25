package config

import (
	"fmt"
	"strings"
)

// HostedEnvironment records that this environment is a specific platform row.
// It is a tuple because a tenant can create rows in another tenant, so neither
// the directory name nor an id alone identifies the row. ManagedCloud means
// lifecycle hosting and is deliberately not reused.
type HostedEnvironment struct {
	APIHost string `yaml:"apihost,omitempty" json:"apiHost,omitempty"`
	// TenantID is resolved from the token issuer, not the local tenant directory.
	TenantID      string `yaml:"tenantid,omitempty" json:"tenantId,omitempty"`
	EnvironmentID string `yaml:"environmentid,omitempty" json:"environmentId,omitempty"`
	// DefinitionRevision is the revision last synced; zero means never synced.
	DefinitionRevision int `yaml:"definitionrevision,omitempty" json:"definitionRevision,omitempty"`
	// DefinitionDigest fingerprints the portable settings at the last transfer, so
	// offline edits are detectable. Empty means "cannot tell", not "matches".
	DefinitionDigest string `yaml:"definitiondigest,omitempty" json:"definitionDigest,omitempty"`
}

func (h HostedEnvironment) IsZero() bool {
	return h == HostedEnvironment{}
}

// HostedEnvironmentIdentity names a platform row without the revision, so a sync
// does not read as a different environment.
type HostedEnvironmentIdentity struct {
	APIHost       string
	TenantID      string
	EnvironmentID string
}

func (h HostedEnvironment) Identity() HostedEnvironmentIdentity {
	return HostedEnvironmentIdentity{
		APIHost:       NormalizeAPIHost(h.APIHost),
		TenantID:      strings.TrimSpace(h.TenantID),
		EnvironmentID: strings.TrimSpace(h.EnvironmentID),
	}
}

func (h HostedEnvironment) SameRow(other HostedEnvironment) bool {
	return h.Identity() == other.Identity()
}

// Describe renders the marker for an operator, host first.
func (h HostedEnvironment) Describe() string {
	if h.IsZero() {
		return "not hosted"
	}
	description := fmt.Sprintf("%s (tenant %s, environment %s)", ValueOrUnset(h.APIHost), ValueOrUnset(h.TenantID), ValueOrUnset(h.EnvironmentID))
	if h.DefinitionRevision > 0 {
		description += fmt.Sprintf(", definition revision %d", h.DefinitionRevision)
	} else {
		description += ", definition not synced"
	}
	return description
}

// NormalizeAPIHost folds a base URL to its host so trailing-slash variants match.
func NormalizeAPIHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return host
}
