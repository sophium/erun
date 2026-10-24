package build

import (
	"io"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
)

type (
	DockerImageBuilderFunc   func(dockerspec.DockerBuildSpec, io.Writer, io.Writer) error
	DockerImageInspectorFunc func(string) (bool, error)
	DockerPushFunc           func(core.Context, dockerspec.DockerPushSpec) error
)
