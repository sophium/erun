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
	line := strings.TrimSuffix(name, "-devops")
	if stock {
		line = "erun"
	}
	reference := name
	if registry != "" {
		reference = registry + "/" + name
	}
	return RuntimeVersionLine{
		Line:      line,
		Image:     reference,
		Disagrees: stock && RuntimeReleaseName(tenant) != DevopsComponentName,
	}
}
