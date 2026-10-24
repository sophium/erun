package build

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/shared/diskheadroom"
)

var buildDiskHeadroomPolicy = diskheadroom.DiskHeadroomPolicy{Label: "build", Refuse: false, Limits: diskheadroom.DiskHeadroomDefaultTimeouts}

// EnsureBuildDiskHeadroom is the build preflight; it warns instead of refusing.
func EnsureBuildDiskHeadroom(ctx core.Context) error {
	diskheadroom.EnsureBuildCacheRetention(ctx, buildDiskHeadroomPolicy)
	return diskheadroom.EnsureDiskHeadroomWith(ctx, buildDiskHeadroomPolicy, diskheadroom.DockerRootDiskBytes, diskheadroom.DockerReclaimableBytes, diskheadroom.DockerBuildCacheReclaimable, diskheadroom.RunDiskHeadroomPrune)
}
