package sshdlink

import (
	"fmt"

	"github.com/sophium/erun/erun-common/base/model"
)

func SshdSyncSummary(dryRun bool, synced model.WorkspaceSyncResult) string {
	if dryRun {
		return fmt.Sprintf("workspace sync would copy %d files, delete %d, and deliver %d artifacts", synced.FilesCopied, synced.FilesDeleted, synced.ArtifactsCopied)
	}
	return fmt.Sprintf("workspace sync copied %d files, deleted %d, delivered %d artifacts", synced.FilesCopied, synced.FilesDeleted, synced.ArtifactsCopied)
}
