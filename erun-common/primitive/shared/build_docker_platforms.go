package shared

import (
	"errors"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// ResolveDockerBuildPlatforms: an override wins, then the env's and the
// project's docker.platforms, then every supported platform.
func ResolveDockerBuildPlatforms(ctx core.Context, projectRoot, environment string, override []string) ([]string, error) {
	if platforms := normalizeDockerPlatforms(override); len(platforms) > 0 {
		return platforms, nil
	}

	if strings.TrimSpace(projectRoot) == "" {
		return slices.Clone(MultiPlatformDockerBuilds), nil
	}

	cfg, _, err := config.LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, config.ErrNotInitialized) {
			return slices.Clone(MultiPlatformDockerBuilds), nil
		}
		return nil, err
	}

	if configured := cfg.DockerPlatformsForEnvironment(environment); len(configured) > 0 {
		ctx.Trace("build: platforms configured as " + strings.Join(configured, ", ") +
			" (.erun/config.yaml " + cfg.DockerPlatformsOrigin(environment) + ")")
		return configured, nil
	}

	return slices.Clone(MultiPlatformDockerBuilds), nil
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
