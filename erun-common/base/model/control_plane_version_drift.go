package model

// Compares each deployed erun-hosted control plane, and the console and docs site it
// links, against the newest version erun's registry has published.

// VersionSurfaceStatus is a console or docs site's deployed version (GET /version.json).
type VersionSurfaceStatus struct {
	URL string `json:"url,omitempty"`
	// An unreachable surface is never reported current; Version/Behind/Ahead stay unset.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Reason explains a Version of "unknown": the surface answered, but not with the
	// expected JSON (often an SPA index.html fallback). It is never Reachable=false.
	Reason string `json:"reason,omitempty"`
	Behind bool   `json:"behind,omitempty"`
	Ahead  bool   `json:"ahead,omitempty"`
}

// ControlPlaneAliasRef is a configured alias that resolved to an already-reported backend.
type ControlPlaneAliasRef struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
}

// ControlPlaneVersionStatus is one control plane's deployed version against the published one.
type ControlPlaneVersionStatus struct {
	Alias  string `json:"alias"`
	APIURL string `json:"apiUrl,omitempty"`
	// AdditionalAliases are other aliases on the same backend, collapsed so drift is not double-counted.
	AdditionalAliases []ControlPlaneAliasRef `json:"additionalAliases,omitempty"`
	// An unreachable plane is never reported current; Version/Behind/Ahead stay unset.
	Reachable         bool   `json:"reachable"`
	UnreachableReason string `json:"unreachableReason,omitempty"`
	Version           string `json:"version,omitempty"`
	// Behind and Ahead are set only when both versions parse as plain semver.
	Behind bool `json:"behind,omitempty"`
	// Ahead means the plane runs a build the registry never published.
	Ahead bool `json:"ahead,omitempty"`
	// Console is nil when the plane reported no consoleUrl.
	Console *VersionSurfaceStatus `json:"console,omitempty"`
	// Docs is nil when the plane reported no docsUrl.
	Docs *VersionSurfaceStatus `json:"docs,omitempty"`
	// AdvertisedAPIURLMismatch is the plane's advertised apiUrl when it resolves to no address
	// the dialed host shares; a host unresolvable on either side yields no verdict.
	AdvertisedAPIURLMismatch string `json:"advertisedApiUrlMismatch,omitempty"`
}

// ControlPlaneVersionDrift is every configured control plane's version status.
type ControlPlaneVersionDrift struct {
	Planes []ControlPlaneVersionStatus `json:"planes,omitempty"`
	// PublishedVersion is read from the registry on every run.
	PublishedVersion string `json:"publishedVersion,omitempty"`
	// PublishedVersionError leaves every plane without a behind/ahead verdict.
	PublishedVersionError string `json:"publishedVersionError,omitempty"`
}
