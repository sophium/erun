package imageregistry

import (
	"context"

	"github.com/sophium/erun/erun-common/base/model"
)

type RuntimeRegistryVersionResolverFunc func(context.Context) (model.RuntimeRegistryVersions, error)
