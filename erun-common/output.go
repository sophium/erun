package eruncommon

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

// BuildResult is the structured result of `erun build`. Version is the content
// identity the build minted — the value an orchestrator threads into
// `erun push <version>` / `erun deploy <version>`. BaseVersion is the stable
// semver without the snapshot suffix, when they differ.
type BuildResult struct {
	Version     string             `json:"version"`
	BaseVersion string             `json:"baseVersion,omitempty"`
	Images      []BuildResultImage `json:"images,omitempty"`
}

// NewBuildResult extracts the structured result from a resolved build execution.
func NewBuildResult(execution BuildExecutionSpec) BuildResult {
	result := BuildResult{}
	if execution.release != nil {
		result.Version = strings.TrimSpace(execution.release.Version)
	}
	for _, build := range execution.dockerBuilds {
		if result.Version == "" {
			result.Version = strings.TrimSpace(build.Image.Version)
			result.BaseVersion = strings.TrimSpace(build.Image.BaseVersion)
		}
		result.Images = append(result.Images, BuildResultImage{
			Name: strings.TrimSpace(build.Image.ImageName),
			Tag:  strings.TrimSpace(build.Image.Tag),
		})
	}
	if result.BaseVersion == result.Version {
		result.BaseVersion = ""
	}
	return result
}
