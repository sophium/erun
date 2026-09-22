package eruncommon

import (
	"errors"
	"slices"
	"strings"
)

// resolveDockerBuildPlatforms picks the docker --platform targets a build
// mints. An explicit override wins outright — either an operator's --platform
// flag, or the full multi-arch pair a release build forces regardless of
// config (see ResolveDockerBuildTarget). Absent one, the project's
// .erun/config.yaml lets a machine permanently pinned to one architecture stop
// paying for the other on every build: environments.<env>.docker.platforms for
// one environment, or the project-wide docker.platforms every environment
// inherits. Absent all of them, every platform erun supports.
func resolveDockerBuildPlatforms(ctx Context, projectRoot, environment string, override []string) ([]string, error) {
	if platforms := normalizeDockerPlatforms(override); len(platforms) > 0 {
		return platforms, nil
	}

	if strings.TrimSpace(projectRoot) == "" {
		return slices.Clone(multiPlatformDockerBuilds), nil
	}

	cfg, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return slices.Clone(multiPlatformDockerBuilds), nil
		}
		return nil, err
	}

	if configured := cfg.DockerPlatformsForEnvironment(environment); len(configured) > 0 {
		ctx.Trace("build: platforms configured as " + strings.Join(configured, ", ") +
			" (.erun/config.yaml " + cfg.DockerPlatformsOrigin(environment) + ")")
		return configured, nil
	}

	return slices.Clone(multiPlatformDockerBuilds), nil
}

func normalizeDockerPlatforms(platforms []string) []string {
	normalized := make([]string, 0, len(platforms))
	for _, platform := range platforms {
		if platform = strings.TrimSpace(platform); platform != "" {
			normalized = append(normalized, platform)
		}
	}
	return normalized
}
