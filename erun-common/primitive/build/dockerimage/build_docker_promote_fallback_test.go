package dockerimage

import (
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
)

func testPromoteBuildInput() imagebuild.DockerBuildSpec {
	return imagebuild.DockerBuildSpec{
		ContextDir:     "/tmp",
		DockerfilePath: "Dockerfile",
		Image: model.DockerImageReference{
			Registry:  "ghcr.io/sophium",
			ImageName: "erun-console",
			Tag:       "ghcr.io/sophium/erun-console:1.0.246",
		},
		Platforms:   []string{"linux/amd64"},
		Push:        true,
		Fingerprint: "abc123",
		Promote:     true,
	}
}
