package open

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// ReclaimOrphanedPortForwardRecords removes records of deleted environments. Best-effort.
func ReclaimOrphanedPortForwardRecords(ctx core.Context) {
	if err := kube.ReclaimOrphanedPortForwardRecords(ctx); err != nil {
		ctx.Trace(fmt.Sprintf("portforward: orphan reclaim skipped: %v", err))
	}
}
