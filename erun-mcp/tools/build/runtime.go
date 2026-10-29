package build

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam/imagebuild"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

func RuntimePushFunc(runtime runtimecfg.RuntimeConfig) imagebuild.DockerPushFunc {
	return func(ctx core.Context, pushInput model.DockerPushSpec) error {
		return push.RunDockerPush(ctx, pushInput, runtime.PushDockerImage)
	}
}
