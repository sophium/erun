package shared

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// The location, and the migration off earlier ones, are shared (erun-common):
// the desktop reads the same files to tell a reachable environment from one
// nobody opened, and `erun delete` has to clear a record wherever it lives.
func PortForwardStatePath(kind, tenant, environment string, dryRun bool) (string, error) {
	return kube.ResolvePortForwardStatePath(kind, tenant, environment, dryRun)
}

func portForwardLogPath(statePath string) string {
	return kube.PortForwardLogPathForState(statePath)
}

// portForwardLogMaxBytes bounds each kubectl port-forward log. A log that
// outgrows it is rolled to its ".1" generation before the next forward appends,
// so the busiest environment cannot fill the disk, while a log under the cap is
// left untouched and stays readable.
const portForwardLogMaxBytes = 5 * 1024 * 1024

// OpenPortForwardLog opens the log a kubectl port-forward writes to, rotating
// an over-cap log first. All three forwards share it so the bounding rule
// cannot drift between them.
func OpenPortForwardLog(logPath string) (*os.File, error) {
	return core.OpenBoundedAppendLog(logPath, portForwardLogMaxBytes, 0o644)
}

// RotatePortForwardLogIfOversized re-applies that cap to a forward that is
// already running. openPortForwardLog bounds the log only when a fresh one is
// opened, and a healthy forward is deliberately reused rather than restarted
// -- it holds the file it opened at start as its own stdout/stderr -- so a
// forward that stays up for weeks never reaches that rotation again and grows
// its log without bound. Called from the paths that find or adopt a live
// forward, so every touch of one re-applies the same cap, and it also reclaims
// a log that had already grown past it before this existed.
//
// Best-effort and silent on failure: rotation is diagnostics housekeeping and
// must never stop a healthy forward from being reused.
func RotatePortForwardLogIfOversized(ctx core.Context, kind, logPath string) {
	if strings.TrimSpace(logPath) == "" {
		return
	}
	if core.RotateOversizedLog(logPath, portForwardLogMaxBytes) {
		ctx.Trace(fmt.Sprintf("%s: rotated oversized port-forward log %s (kept a %s.1 backup)", kind, logPath, logPath))
	}
}

// ReclaimOrphanedPortForwardRecords removes forward records whose environment
// no longer exists, so the tree tracks the environments it describes instead
// of growing forever. It runs where the forward store is already touched --
// the same forward setup that applies the log cap -- because a reclaim left to
// some unrelated command is a reclaim that stops happening the moment that
// command changes.
//
// Best-effort and silent on failure: reclaiming disk is diagnostics
// housekeeping and must never stop an operator opening the environment they
// asked for.
func ReclaimOrphanedPortForwardRecords(ctx core.Context) {
	if err := kube.ReclaimOrphanedPortForwardRecords(ctx); err != nil {
		ctx.Trace(fmt.Sprintf("portforward: orphan reclaim skipped: %v", err))
	}
}
