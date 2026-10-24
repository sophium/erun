package portfwd

import (
	"fmt"
	"os"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
)

// PortForwardStatePath is shared with the desktop and `erun delete`, which read the same records.
func PortForwardStatePath(kind, tenant, environment string, dryRun bool) (string, error) {
	return kube.ResolvePortForwardStatePath(kind, tenant, environment, dryRun)
}

func PortForwardLogPath(statePath string) string {
	return kube.PortForwardLogPathForState(statePath)
}

// portForwardLogMaxBytes caps each port-forward log before it is rolled to ".1".
const portForwardLogMaxBytes = 5 * 1024 * 1024

// OpenPortForwardLog opens a port-forward log, rotating it first if over the cap.
func OpenPortForwardLog(logPath string) (*os.File, error) {
	return core.OpenBoundedAppendLog(logPath, portForwardLogMaxBytes, 0o644)
}

// RotatePortForwardLogIfOversized re-applies the cap for a long-lived reused forward,
// which never reopens its log. Best-effort.
func RotatePortForwardLogIfOversized(ctx core.Context, kind, logPath string) {
	if strings.TrimSpace(logPath) == "" {
		return
	}
	if core.RotateOversizedLog(logPath, portForwardLogMaxBytes) {
		ctx.Trace(fmt.Sprintf("%s: rotated oversized port-forward log %s (kept a %s.1 backup)", kind, logPath, logPath))
	}
}
