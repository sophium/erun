package cloudcli

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/seam/cloudstore"
	"github.com/sophium/erun/erun-common/infra/cloud/cloudcontext"
	"github.com/sophium/erun/erun-common/infra/cloud/contextpower"
)

func WithCloudContextPreflight(ctx core.Context, store any) core.Context {
	cloudStore, ok := store.(cloudstore.CloudContextStore)
	if !ok {
		return ctx
	}
	ctx.KubernetesContextPreflight = contextpower.CloudContextPreflight(cloudStore, cloudcontext.CloudContextDependencies{})
	return ctx
}
