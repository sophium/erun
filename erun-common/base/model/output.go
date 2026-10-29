package model

// OutputMode selects how a command renders its result.
type OutputMode string

const (
	OutputText OutputMode = "text"
	OutputJSON OutputMode = "json"
)

// BuildResultImage is one image produced by a build.
type BuildResultImage struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// BuildResultChart is one Helm chart the build packaged.
type BuildResultChart struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// BuildResult is the structured result of `erun build`. An empty Version with
// non-empty Scripts is a script-only build, which mints no image version.
type BuildResult struct {
	Version     string             `json:"version"`
	BaseVersion string             `json:"baseVersion,omitempty"`
	Images      []BuildResultImage `json:"images,omitempty"`
	Charts      []BuildResultChart `json:"charts,omitempty"`
	Scripts     []string           `json:"scripts,omitempty"`
	// Packages mint no image version either.
	Packages []string `json:"packages,omitempty"`
}
