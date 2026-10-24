package deploy

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// RuntimeVersionLine names which release line an environment's persisted
// RuntimeVersion number belongs to. The number alone cannot tell an erun
// version apart from a tenant's own devops line -- two environments in the
// same tenant can even ride different lines from each other -- so `erun
// list` renders this alongside runtime-version rather than the bare number
// (erun#1746).
type RuntimeVersionLine struct {
	// Line is "erun" for erun's own release line, or the tenant/component
	// name (e.g. "frs") for a tenant's own devops line. Empty when
	// Undetermined.
	Line string `json:"line,omitempty"`
	// Image is the registry-qualified image name (no tag) the line's number
	// refers to, e.g. "ghcr.io/sophium/frs-devops".
	Image string `json:"image,omitempty"`
	// Undetermined is true when no deploy has recorded a resolved runtime
	// image for this environment, so the line genuinely cannot be told from
	// the persisted config -- callers must render this distinctly rather
	// than guess a line from the tenant name.
	Undetermined bool `json:"undetermined,omitempty"`
	// Disagrees is true when the environment's own Helm release name
	// (<tenant>-devops) differs from the image's component name while the
	// image is still erun's own stock image -- legitimate, but the case
	// erun#1746 was filed over: the row most likely to be misread.
	Disagrees bool `json:"disagrees,omitempty"`
}

// ResolveRuntimeVersionLine reports which release line tenant/env's persisted
// RuntimeVersion belongs to, from the last image a deploy actually recorded
// for the environment (EnvConfig.RuntimeRunningImage) -- never from tenant or
// release naming conventions alone, which is the exact inference erun#1746
// was filed to stop (a release named "<tenant>-devops" can run erun's own
// stock image, and vice versa). Callers should skip rendering this entirely
// when RuntimeVersion itself is empty -- there is no version to annotate.
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

// ErunVersion is the second version number the environment hover card needs
// beside RuntimeVersionLine: the erun version an environment's runtime chart
// carries (EnvConfig.RuntimeChart), which can differ from RuntimeVersion
// whenever the runtime image itself rides a tenant's own release line.
type ErunVersion struct {
	Version string `json:"version,omitempty"`
	// SameAsRuntimeVersion is true when the chart states no version of its own
	// and the runtime image is confirmed on erun's own release line -- the case
	// where a second, identical-looking number would be redundant rather than
	// informative.
	SameAsRuntimeVersion bool `json:"sameAsRuntimeVersion,omitempty"`
}

// ResolveErunVersion resolves EnvConfig.RuntimeChart to the erun version this
// environment's runtime rides, from config alone -- no live probe, the same
// "readable from config alone" contract EnvConfig.RuntimeImageLineMismatch
// relies on. runtimeLine is the RuntimeVersionLine already resolved for the
// same environment (nil when RuntimeVersion itself is empty); it is what
// tells this function whether an empty RuntimeChart is safe to read as
// "follows RuntimeVersion" -- true only once the running image is confirmed
// on erun's own line -- rather than a guess. Returns nil whenever the erun
// version cannot be told apart from a guess, per the same "never guess a
// line" rule RuntimeVersionLine already follows.
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
