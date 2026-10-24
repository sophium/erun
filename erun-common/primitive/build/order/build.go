package order

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/seam"
)

func DockerBuildsByTag(builds []seam.DockerBuildSpec) map[string]seam.DockerBuildSpec {
	buildsByTag := make(map[string]seam.DockerBuildSpec, len(builds))
	for _, build := range builds {
		buildsByTag[strings.TrimSpace(build.Image.Tag)] = build
	}
	return buildsByTag
}
