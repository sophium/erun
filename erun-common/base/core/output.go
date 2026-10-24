package core

import (
	"fmt"
	"strings"
)

// OutputMode selects how a command renders its result: Text is the default
// human stream, JSON a structured result for orchestrators to capture.
type OutputMode string

const (
	OutputText OutputMode = "text"
	OutputJSON OutputMode = "json"
)

// ParseOutputMode validates a raw --output value. Empty resolves to text so an
// unset flag keeps today's behaviour.
func ParseOutputMode(raw string) (OutputMode, error) {
	switch strings.TrimSpace(strings.ToLower(raw)) {
	case "", string(OutputText):
		return OutputText, nil
	case string(OutputJSON):
		return OutputJSON, nil
	default:
		return OutputText, fmt.Errorf("invalid --output value %q: expected %q or %q", raw, OutputText, OutputJSON)
	}
}

// BuildResultImage is one image produced by a build: its name and the
// registry-qualified tag an orchestrator would push or reference.
type BuildResultImage struct {
	Name string `json:"name"`
	Tag  string `json:"tag"`
}

// BuildResultChart is one Helm chart the build packaged from <tenant>-devops/k8s/*,
// with the version it was packaged at (the value push publishes it to OCI under).
type BuildResultChart struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// BuildResult is the structured result of `erun build`. Version is the content
// identity the build minted — the value an orchestrator threads into
// `erun push <version>` / `erun deploy <version>`. BaseVersion is the stable
// semver without the snapshot suffix, when they differ.
// Scripts names the project build scripts the execution ran instead of images.
// A caller reads the plan from this result rather than the trace: an empty
// Version beside a non-empty Scripts is a script-only build, which mints no
// image version and tests nothing an image's build/test stage would have tested.
type BuildResult struct {
	Version     string             `json:"version"`
	BaseVersion string             `json:"baseVersion,omitempty"`
	Images      []BuildResultImage `json:"images,omitempty"`
	Charts      []BuildResultChart `json:"charts,omitempty"`
	Scripts     []string           `json:"scripts,omitempty"`
	// Packages names the linux package components the build tested; like
	// Scripts they mint no image version.
	Packages []string `json:"packages,omitempty"`
}
