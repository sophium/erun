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
		APIHost:       normalizeAPIHost(h.APIHost),
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
	description := fmt.Sprintf("%s (tenant %s, environment %s)", valueOrUnset(h.APIHost), valueOrUnset(h.TenantID), valueOrUnset(h.EnvironmentID))
	if h.DefinitionRevision > 0 {
		description += fmt.Sprintf(", definition revision %d", h.DefinitionRevision)
	} else {
		description += ", definition not synced"
	}
	return description
}

// HostedEnvironmentFromConfig returns the marker; one without an environment id
// is a half-written record and reports false.
func HostedEnvironmentFromConfig(config EnvConfig) (HostedEnvironment, bool) {
	if config.Hosted.IsZero() || strings.TrimSpace(config.Hosted.EnvironmentID) == "" {
		return HostedEnvironment{}, false
	}
	return config.Hosted, true
}

// HostedMarkerMismatchError refuses a transfer whose resolved row differs from the
// marker, rather than re-pointing the environment at another row.
type HostedMarkerMismatchError struct {
	// Field is "api host", "tenant" or "environment".
	Field    string
	Recorded string
	Resolved string
}

func (e *HostedMarkerMismatchError) Error() string {
	return fmt.Sprintf(
		"this environment is marked as hosted with %s %s, but this call resolved %s: refusing to transfer the definition. "+
			"Re-point it with `erun platform env pull --environment-id <id>` against the platform you meant, or clear the marker by hand if the environment is no longer hosted.",
		e.Field, valueOrUnset(e.Recorded), valueOrUnset(e.Resolved))
}

// ErrEnvironmentNotHosted means the environment carries no hosted marker.
var ErrEnvironmentNotHosted = fmt.Errorf("this environment is not marked as hosted")

// CheckHostedMarkerTarget refuses a transfer to a row other than the marker's.
// An empty resolved environment id means not yet resolved and is skipped.
func CheckHostedMarkerTarget(marker HostedEnvironment, resolvedAPIHost, resolvedTenantID, resolvedEnvironmentID string) error {
	recorded := marker.Identity()
	if recorded.APIHost == "" || recorded.TenantID == "" {
		return nil
	}
	if host := normalizeAPIHost(resolvedAPIHost); host != "" && host != recorded.APIHost {
		return &HostedMarkerMismatchError{Field: "api host", Recorded: recorded.APIHost, Resolved: host}
	}
	if tenant := strings.TrimSpace(resolvedTenantID); tenant != "" && tenant != recorded.TenantID {
		return &HostedMarkerMismatchError{Field: "tenant", Recorded: recorded.TenantID, Resolved: tenant}
	}
	if environmentID := strings.TrimSpace(resolvedEnvironmentID); environmentID != "" && environmentID != recorded.EnvironmentID {
		return &HostedMarkerMismatchError{Field: "environment", Recorded: recorded.EnvironmentID, Resolved: environmentID}
	}
	return nil
}

// normalizeAPIHost folds a base URL to its host so trailing-slash variants match.
func normalizeAPIHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return host
}

// HostedDefinitionDrift compares the local and platform definition revisions.
type HostedDefinitionDrift struct {
	LocalRevision    int
	PlatformRevision int
}

func (d HostedDefinitionDrift) IsBehind() bool { return d.PlatformRevision > d.LocalRevision }

// HasLocalEdits reports local edits since the last sync; with IsBehind, that is
// a two-sided edit a pull must ask about.
func (d HostedDefinitionDrift) HasLocalEdits() bool {
	return d.LocalRevision > 0 && d.LocalRevision < d.PlatformRevision
}

// HostedDefinitionLocalChange reports, from the marker digest alone, whether the
// portable settings moved since the last transfer; it works offline.
type HostedDefinitionLocalChange struct {
	// Available is false when the marker records no digest.
	Available bool
	Changed   bool
}

func HostedDefinitionLocalChangeFor(config EnvConfig) HostedDefinitionLocalChange {
	marker, ok := HostedEnvironmentFromConfig(config)
	if !ok || marker.DefinitionDigest == "" {
		return HostedDefinitionLocalChange{}
	}
	return HostedDefinitionLocalChange{
		Available: true,
		Changed:   DefinitionDigest(BuildPlatformEnvDefinition(config)) != marker.DefinitionDigest,
	}
}

// HasUnsentChange reports whether an upload is warranted; it is true without a
// digest because uploading once starts tracking.
func (c HostedDefinitionLocalChange) HasUnsentChange() bool { return !c.Available || c.Changed }

// Describe renders the change for an operator; CLI and desktop share the wording.
func (c HostedDefinitionLocalChange) Describe() string {
	switch {
	case !c.Available:
		return "this machine has no record of what it last sent, so it cannot tell whether its settings have moved"
	case c.Changed:
		return "this machine's settings have changed since they were last sent to the platform"
	default:
		return "this machine's settings match what was last sent to the platform"
	}
}

func (d HostedDefinitionDrift) Describe() string {
	switch {
	case d.PlatformRevision == 0:
		return "the platform holds no definition for this environment"
	case d.LocalRevision == 0:
		return fmt.Sprintf("the platform holds definition revision %d and this machine has never pulled it", d.PlatformRevision)
	case d.IsBehind():
		return fmt.Sprintf("the platform is at definition revision %d and this machine last pulled revision %d", d.PlatformRevision, d.LocalRevision)
	default:
		return fmt.Sprintf("this machine is up to date at definition revision %d", d.LocalRevision)
	}
}
