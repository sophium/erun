package build

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/build"
	"github.com/sophium/erun/erun-common/primitive/push"
	"github.com/sophium/erun/erun-common/primitive/shared/dockerspec"
	"github.com/sophium/erun/erun-mcp/tools/shared/runtimecfg"
)

func RuntimePushFunc(runtime runtimecfg.RuntimeConfig) build.DockerPushFunc {
	return func(ctx core.Context, pushInput dockerspec.DockerPushSpec) error {
		return push.RunDockerPush(ctx, pushInput, runtime.PushDockerImage)
	}
}
