package model

import (
	"strings"
)

// One erun version is recorded in several places in a tenant repo and they must agree.
// Sites are found by pattern rather than a fixed layout, since tenants add modules and umbrellas.

// PinSiteKind names what kind of reference a site is.
type PinSiteKind string

const (
	PinSiteRuntimeVersion PinSiteKind = "runtime-version"
	PinSiteTerraformRef   PinSiteKind = "terraform-ref"
	PinSiteHelmDependency PinSiteKind = "helm-dependency"
	PinSiteRuntimeImage   PinSiteKind = "runtime-image"
	PinSiteImageReference PinSiteKind = "image-reference"
	PinSiteRuntimeChart   PinSiteKind = "runtime-chart"
)

// PinSite is one place a version is recorded, and what it would become.
type PinSite struct {
	Kind PinSiteKind `json:"kind"`
	// Path is repo-relative, or empty for the environment's own config.
	Path    string `json:"path,omitempty"`
	Detail  string `json:"detail,omitempty"`
	Current string `json:"current"`
	Target  string `json:"target"`
}

// Aligned reports whether this site already holds the target.
func (s PinSite) Aligned() bool {
	return strings.TrimSpace(s.Current) == strings.TrimSpace(s.Target)
}

// PinPlan is every site a re-pin would touch, resolved before anything is written.
type PinPlan struct {
	Tenant      string    `json:"tenant"`
	Environment string    `json:"environment"`
	Target      string    `json:"target"`
	Previous    string    `json:"previous,omitempty"`
	ProjectRoot string    `json:"projectRoot"`
	Sites       []PinSite `json:"sites"`
	// Skipped explains why apparent version references were left out of Sites.
	Skipped []string `json:"skipped,omitempty"`
	// ProjectRootNote is set only when ProjectRoot fell back to the caller's working directory.
	ProjectRootNote string `json:"projectRootNote,omitempty"`
	// BaseFreshness is how far the checkout is behind its tracked ref; nil when none could be read.
	BaseFreshness *PinBaseFreshness `json:"baseFreshness,omitempty"`
}

// Changes is the sites that would actually move; empty means a no-op.
func (p PinPlan) Changes() []PinSite {
	changes := make([]PinSite, 0, len(p.Sites))
	for _, site := range p.Sites {
		if !site.Aligned() {
			changes = append(changes, site)
		}
	}
	return changes
}

func (p PinPlan) Aligned() bool { return len(p.Changes()) == 0 }
