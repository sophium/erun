package build

import (
	"github.com/sophium/erun/erun-common/base/core"
	sharedpkg "github.com/sophium/erun/erun-common/primitive/shared"
)

var buildDiskHeadroomPolicy = sharedpkg.DiskHeadroomPolicy{Label: "build", Refuse: false, Limits: sharedpkg.DiskHeadroomDefaultTimeouts}

// EnsureBuildDiskHeadroom is the build preflight; it warns instead of refusing.
func EnsureBuildDiskHeadroom(ctx core.Context) error {
	sharedpkg.EnsureBuildCacheRetention(ctx, buildDiskHeadroomPolicy)
	return sharedpkg.EnsureDiskHeadroomWith(ctx, buildDiskHeadroomPolicy, sharedpkg.DockerRootDiskBytes, sharedpkg.DockerReclaimableBytes, sharedpkg.DockerBuildCacheReclaimable, sharedpkg.RunDiskHeadroomPrune)
}
