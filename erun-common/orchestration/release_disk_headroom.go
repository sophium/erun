package orchestration

import (
	"github.com/sophium/erun/erun-common/base/core"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
)

var buildDiskHeadroomPolicy = sharedpkg.DiskHeadroomPolicy{Label: "build", Refuse: false, Limits: sharedpkg.DiskHeadroomDefaultTimeouts}

// ensureBuildDiskHeadroom is the same preflight for an ordinary build, which
// warns instead of refusing. It is the one that actually runs between releases,
// where the cache growth that fills a node happens.
func ensureBuildDiskHeadroom(ctx core.Context) error {
	sharedpkg.EnsureBuildCacheRetention(ctx, buildDiskHeadroomPolicy)
	return sharedpkg.EnsureDiskHeadroomWith(ctx, buildDiskHeadroomPolicy, sharedpkg.DockerRootDiskBytes, sharedpkg.DockerReclaimableBytes, sharedpkg.DockerBuildCacheReclaimable, sharedpkg.RunDiskHeadroomPrune)
}
