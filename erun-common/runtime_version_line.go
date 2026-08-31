package eruncommon

import "strings"

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
func ResolveRuntimeVersionLine(tenant string, env EnvConfig) RuntimeVersionLine {
	image := strings.TrimSpace(env.RuntimeRunningImage)
	if image == "" {
		return RuntimeVersionLine{Undetermined: true}
	}
	registry, name, _, ok := parseDockerImageReference(image)
	name = strings.TrimSpace(name)
	if !ok || name == "" {
		return RuntimeVersionLine{Undetermined: true}
	}
	stock := name == DefaultRuntimeImageName
	reference := name
	if registry != "" {
		reference = registry + "/" + name
	}
	return RuntimeVersionLine{
		Line:      imageReleaseLine(name),
		Image:     reference,
		Disagrees: stock && RuntimeReleaseName(tenant) != DevopsComponentName,
	}
}

// imageReleaseLine names the release line a bare component name belongs to:
// "erun" for the stock erun-devops image, or the tenant/component name
// (stripping "-devops") for any other <name>-devops image.
func imageReleaseLine(name string) string {
	if name == DefaultRuntimeImageName {
		return "erun"
	}
	return strings.TrimSuffix(name, "-devops")
}

// runtimeImageComponentName extracts the bare component name from a runtime
// image reference in any shape RuntimeImage/RuntimeRunningImage may take: a
// bare name ("erun-devops"), a registry-qualified tagless pin
// ("ghcr.io/sophium/frs-devops", the self-maintaining form
// stripRuntimeImageTag/resolveRuntimeImageOverride round-trip), or a fully
// resolved tagged reference ("ghcr.io/sophium/frs-devops:1.0.86"). Empty
// when image is empty.
func runtimeImageComponentName(image string) string {
	image = strings.TrimSpace(image)
	if image == "" {
		return ""
	}
	if idx := strings.LastIndex(image, "/"); idx >= 0 {
		image = image[idx+1:]
	}
	if idx := strings.Index(image, "@"); idx >= 0 {
		image = image[:idx]
	}
	if idx := strings.LastIndex(image, ":"); idx >= 0 {
		image = image[:idx]
	}
	return strings.TrimSpace(image)
}

// runtimeImageReleaseLine classifies a runtime image reference in any shape
// (see runtimeImageComponentName) by release line. ok is false when image is
// empty or names no component -- callers must treat that as undetermined,
// never guess a line from convention alone.
func runtimeImageReleaseLine(image string) (line string, ok bool) {
	name := runtimeImageComponentName(image)
	if name == "" {
		return "", false
	}
	return imageReleaseLine(name), true
}
