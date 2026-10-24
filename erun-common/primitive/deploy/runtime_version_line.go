package deploy

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

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

// ResolveRuntimeVersionLine derives the line from the image a deploy recorded,
// never from naming conventions.
func ResolveRuntimeVersionLine(tenant string, env config.EnvConfig) RuntimeVersionLine {
	image := strings.TrimSpace(env.RuntimeRunningImage)
	if image == "" {
		return RuntimeVersionLine{Undetermined: true}
	}
	registry, name, _, ok := shared.ParseDockerImageReference(image)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return RuntimeVersionLine{Undetermined: true}
	}
	stock := name == config.DefaultRuntimeImageName
	reference := name
	if registry != "" {
		reference = registry + "/" + name
	}
	return RuntimeVersionLine{
		Line:      config.ImageReleaseLine(name),
		Image:     reference,
		Disagrees: stock && config.RuntimeReleaseName(tenant) != config.DevopsComponentName,
	}
}

// ErunVersion is the erun version of the runtime chart, which differs from
// RuntimeVersion when the image rides a tenant's own line.
type ErunVersion struct {
	Version string `json:"version,omitempty"`
	// SameAsRuntimeVersion marks a redundant number: unversioned chart, erun image.
	SameAsRuntimeVersion bool `json:"sameAsRuntimeVersion,omitempty"`
}

// ResolveErunVersion reads config alone and returns nil rather than guess; an
// empty RuntimeChart follows RuntimeVersion only on erun's own line.
func ResolveErunVersion(env config.EnvConfig, runtimeLine *RuntimeVersionLine) *ErunVersion {
	runtimeVersion := strings.TrimSpace(env.RuntimeVersion)
	if runtimeVersion == "" {
		return nil
	}
	chart := strings.TrimSpace(env.RuntimeChart)
	if chart == "" {
		if runtimeLine == nil || runtimeLine.Line != "erun" {
			return nil
		}
		return &ErunVersion{Version: runtimeVersion, SameAsRuntimeVersion: true}
	}
	_, chartVersion := SplitChartReference(chart)
	chartVersion = strings.TrimSpace(chartVersion)
	if chartVersion == "" {
		return nil
	}
	return &ErunVersion{Version: chartVersion}
}
