package cloudcli

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
)

func WithCloudContextPreflight(ctx core.Context, store any) core.Context {
	cloudStore, ok := store.(cloud.CloudContextStore)
	if !ok {
		return ctx
	}
	ctx.KubernetesContextPreflight = cloud.CloudContextPreflight(cloudStore, cloud.CloudContextDependencies{})
	return ctx
}
