package order

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
)

func DockerBuildsByTag(builds []imagebuild.DockerBuildSpec) map[string]imagebuild.DockerBuildSpec {
	buildsByTag := make(map[string]imagebuild.DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}
