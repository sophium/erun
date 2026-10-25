package build

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/base/seam"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

func RuntimePushFunc(runtime runtimecfg.RuntimeConfig) seam.DockerPushFunc {
	return func(ctx core.Context, pushInput model.DockerPushSpec) error {
		return push.RunDockerPush(ctx, pushInput, runtime.PushDockerImage)
	}
}
