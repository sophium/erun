package releasespec

import (
	"fmt"
	"slices"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

func ResolveDockerBuildTarget(findProjectRoot seam.ProjectFinderFunc, target model.DockerCommandTarget) (model.DockerCommandTarget, *model.ReleaseSpec, error) {
	target.VersionOverride = strings.TrimSpace(target.VersionOverride)
	if !target.Release {
		return target, nil, nil
	}
	if target.VersionOverride != "" {
		return model.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with explicit version override")
	}
	if len(target.Platforms) > 0 {
		return model.DockerCommandTarget{}, nil, fmt.Errorf("release build cannot be combined with an explicit --platform override: a release always publishes every platform erun supports")
	}

	// A zero-value Logger drops traces.
	releaseSpec, err := ResolveReleaseSpec(core.Context{}, findProjectRoot, ReleaseParams{ProjectRoot: target.ProjectRoot, Force: target.Force})
	if err != nil {
		return model.DockerCommandTarget{}, nil, err
	}

	target.Release = false
	target.VersionOverride = releaseSpec.Version
	// A release builds every platform, regardless of any docker.platforms pin.
	target.Platforms = slices.Clone(dockerspec.MultiPlatformDockerBuilds)
	return target, &releaseSpec, nil
}
