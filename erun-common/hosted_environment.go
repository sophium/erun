package eruncommon

import (
	"fmt"
	"strings"
)

// HostedEnvironment is the local record that this environment corresponds to a
// row on a hosted platform — the marker a definition transfer hangs off.
//
// On the platform every row is hosted by definition, so "is hosted" cannot be
// server state: it is a local assertion that *this* local environment is the
// same thing as *that* platform row. It is a tuple rather than a bare id
// because no single element is enough to say which row. Tenants are resolved
// from the token issuer and a create body may name a tenant other than the
// caller's own, so a directory name cannot stand in for the platform tenant;
// two platforms can therefore hold rows that share an environment id only in
// the sense that they are unrelated. The api host names which plane, the tenant
// id names which tenant on it, the environment id names the row, and the
// revision says how far the local copy has been brought up to date.
//
// ManagedCloud is deliberately not reused for this. It already means "the
// platform manages this environment's lifecycle", it is set automatically for a
// remote worktree carrying a cloud alias, and it says nothing about a definition
// being synced; overloading it would make lifecycle hosting and definition
// hosting indistinguishable.
type HostedEnvironment struct {
	// APIHost is the erun-backend-api base URL the row lives on, as resolved
	// for the cloud alias the call went through.
	APIHost string `yaml:"apihost,omitempty" json:"apiHost,omitempty"`
	// TenantID is the tenant the platform resolved from the token issuer, not
	// the tenant directory this config happens to live under.
	TenantID string `yaml:"tenantid,omitempty" json:"tenantId,omitempty"`
	// EnvironmentID is the platform row's environment_id.
	EnvironmentID string `yaml:"environmentid,omitempty" json:"environmentId,omitempty"`
	// DefinitionRevision is the definition revision this local copy was last
	// synced from. Zero means the local copy has never been synced: the
	// environment was registered with a definition (revision 1) but its local
	// config has not been pulled since, or the marker predates this field.
	DefinitionRevision int `yaml:"definitionrevision,omitempty" json:"definitionRevision,omitempty"`
}

// IsZero reports whether no marker is recorded, so a config that has never been
// hosted omits the block entirely on write.
func (h HostedEnvironment) IsZero() bool {
	return h == HostedEnvironment{}
}

// HostedEnvironmentIdentity is the part of a marker that names a platform row,
// with the revision left out. Two markers sharing an identity describe the same
// row however far apart their revisions are; a comparison that included the
// revision would read a sync as a different environment.
type HostedEnvironmentIdentity struct {
	APIHost       string
	TenantID      string
	EnvironmentID string
}

// Identity returns the marker's row identity.
func (h HostedEnvironment) Identity() HostedEnvironmentIdentity {
	return HostedEnvironmentIdentity{
		APIHost:       normalizeAPIHost(h.APIHost),
		TenantID:      strings.TrimSpace(h.TenantID),
		EnvironmentID: strings.TrimSpace(h.EnvironmentID),
	}
}

// SameRow reports whether this marker and other name the same platform row.
func (h HostedEnvironment) SameRow(other HostedEnvironment) bool {
	return h.Identity() == other.Identity()
}

// Describe renders the marker for an operator, leading with the host so the
// line reads as "where", not as three opaque ids.
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

// HostedEnvironmentFromConfig returns the environment's marker and whether one
// is recorded at all. A config carrying no marker — or a marker with no
// environment id, which is what a half-written record looks like — reports
// false, so callers treat it as "not hosted" rather than as "hosted somewhere
// unknown".
func HostedEnvironmentFromConfig(config EnvConfig) (HostedEnvironment, bool) {
	if config.Hosted.IsZero() || strings.TrimSpace(config.Hosted.EnvironmentID) == "" {
		return HostedEnvironment{}, false
	}
	return config.Hosted, true
}

// HostedMarkerMismatchError is the refusal an upload or a pull raises when the
// platform it resolved is not the row the local marker records. Adopting the
// resolved identity instead would silently re-point the environment at a
// different tenant's row — or at a same-named row on a different plane — and
// the definition would then be written where it does not belong.
type HostedMarkerMismatchError struct {
	// Field is the element that disagreed: "api host", "tenant" or
	// "environment".
	Field string
	// Recorded is what the local marker says, Resolved what this call resolved.
	Recorded string
	Resolved string
}

func (e *HostedMarkerMismatchError) Error() string {
	return fmt.Sprintf(
		"this environment is marked as hosted with %s %s, but this call resolved %s: refusing to transfer the definition. "+
			"Re-point it with `erun platform env pull --environment-id <id>` against the platform you meant, or clear the marker by hand if the environment is no longer hosted.",
		e.Field, valueOrUnset(e.Recorded), valueOrUnset(e.Resolved))
}

// ErrEnvironmentNotHosted is returned when an operation requires the hosted
// marker — a definition upload, a drift check — and the environment carries
// none.
var ErrEnvironmentNotHosted = fmt.Errorf("this environment is not marked as hosted")

// CheckHostedMarkerTarget refuses a transfer whose resolved platform row is not
// the one the local marker records.
//
// It compares the api host and the tenant, and the environment id when the
// caller resolved one. An empty resolved environment id means "not resolved
// yet" — an upload before the adopt call has returned, for instance — not a
// disagreement, so it is not checked.
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

// normalizeAPIHost folds a base URL down to the host an operator would
// recognize, so a marker written from "https://api.erunpaas.com/" and a
// resolution that produced "https://api.erunpaas.com" do not read as two
// platforms.
func normalizeAPIHost(raw string) string {
	host := strings.TrimSpace(raw)
	host = strings.TrimSuffix(host, "/")
	host = strings.TrimPrefix(host, "https://")
	host = strings.TrimPrefix(host, "http://")
	return host
}

// HostedDefinitionDrift describes how far a local copy has fallen behind the
// definition the platform holds.
type HostedDefinitionDrift struct {
	// LocalRevision is the revision the local copy was synced from.
	LocalRevision int
	// PlatformRevision is the revision the platform now holds.
	PlatformRevision int
}

// IsBehind reports whether the platform has moved past the local copy.
func (d HostedDefinitionDrift) IsBehind() bool { return d.PlatformRevision > d.LocalRevision }

// HasLocalEdits reports whether the local copy has been edited since it was
// last synced, which is the other half of a two-sided edit: the platform moving
// ahead on its own is a plain catch-up, but the platform moving ahead while the
// local copy also moved is the case a pull must ask about rather than decide.
func (d HostedDefinitionDrift) HasLocalEdits() bool {
	return d.LocalRevision > 0 && d.LocalRevision < d.PlatformRevision
}

// Describe renders the drift for an operator.
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
