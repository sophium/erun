package model

// RuntimeVersionLine names the release line (erun or a tenant's devops line)
// that a persisted RuntimeVersion number belongs to.
type RuntimeVersionLine struct {
	// Line is "erun" or the tenant/component name, e.g. "frs".
	Line string `json:"line,omitempty"`
	// Image is registry-qualified, without tag.
	Image string `json:"image,omitempty"`
	// Undetermined: no deploy recorded a runtime image; callers must not guess
	// a line from the tenant name.
	Undetermined bool `json:"undetermined,omitempty"`
	// Disagrees: the Helm release name differs from erun's stock image name,
	// which is legitimate but easily misread.
	Disagrees bool `json:"disagrees,omitempty"`
}

// ErunVersion is the erun version of the runtime chart, which differs from
// RuntimeVersion when the image rides a tenant's own line.
type ErunVersion struct {
	Version string `json:"version,omitempty"`
	// SameAsRuntimeVersion marks a redundant number: unversioned chart, erun image.
	SameAsRuntimeVersion bool `json:"sameAsRuntimeVersion,omitempty"`
}
