package appsession

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/activity/remotesession"
)

const (
	RemoteShellReattachDeployExitCode = 75
	RemoteShellTakenOverExitCode      = 76
)

// ShellSessionTakenOverNotice is matched verbatim by the desktop to stop its
// reconnect loop; the wording is a public contract.
const ShellSessionTakenOverNotice = "open: session re-attached in another ERun window"

// ResolveAISessionAttachOutcome classifies the attach script's exit status.
func ResolveAISessionAttachOutcome(exitCode int, exitCodeKnown bool) model.AISessionAttachOutcome {
	if !exitCodeKnown {
		return model.AISessionAttachOutcomeUnknown
	}
	switch exitCode {
	case RemoteShellTakenOverExitCode:
		return model.AISessionAttachOutcomeTakenOver
	case RemoteShellReattachDeployExitCode:
		return model.AISessionAttachOutcomeDeployReattach
	default:
		return model.AISessionAttachOutcomeEnded
	}
}

// ParseRemoteAppSessionIDs extracts this tenant+env's persistent desktop session ids.
func ParseRemoteAppSessionIDs(tenant, environment, lsOutput string) []string {
	prefix := core.SanitizeForFilename(tenant) + "-" + core.SanitizeForFilename(environment) + "-"
	var ids []string
	for _, raw := range strings.Split(lsOutput, "\n") {
		name := strings.TrimSpace(raw)
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".dtach") {
			continue
		}
		if id := strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".dtach"); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// RemoteAppSessionAttachLines attaches or creates a dtach session and evicts
// other viewers; launchCommand runs only on create. redraw "winch" is needed
// for a TUI like Claude that ignores ^L.
func RemoteAppSessionAttachLines(socket, redraw, launchCommand string) []string {
	owner := strings.TrimSuffix(socket, ".dtach") + ".owner"
	lines := []string{
		// Kill other dtach clients, never the master (none if it is unknown);
		// evicted wrappers exit 76 instead of reconnecting.
		"attach_id=\"$$-$(date +%s)\"",
		fmt.Sprintf("printf '%%s' \"$attach_id\" > \"%s\"", owner),
	}
	lines = append(lines, remotesession.RemoteAppSessionMasterScanLines(socket)...)
	return append(lines,
		fmt.Sprintf("if [ -S \"%s\" ] && [ -n \"$master_pid\" ]; then for dtach_pid in $(pgrep -x dtach 2>/dev/null || true); do if [ \"$dtach_pid\" != \"$master_pid\" ] && grep -qF \"%s\" \"/proc/$dtach_pid/cmdline\" 2>/dev/null; then kill \"$dtach_pid\" 2>/dev/null || true; fi; done; fi", socket, socket),
		fmt.Sprintf("dtach -A \"%s\" -r %s %s || shell_status=$?", socket, redraw, launchCommand),
		fmt.Sprintf("if [ \"$(cat \"%s\" 2>/dev/null)\" != \"$attach_id\" ]; then exit %d; fi", owner, RemoteShellTakenOverExitCode),
	)
}

// RemoteAppSessionSocketPath returns the dtach socket path for a session.
func RemoteAppSessionSocketPath(tenant, environment, id string) string {
	return fmt.Sprintf("%s/%s-%s-%s.dtach", remotesession.RemoteAppSessionSocketDir, core.SanitizeForFilename(tenant), core.SanitizeForFilename(environment), core.SanitizeForFilename(id))
}

// RemoteAppSessionEndScript permanently ends a session; only an explicit tab
// close runs it.
func RemoteAppSessionEndScript(tenant, environment, id string) string {
	socket := RemoteAppSessionSocketPath(tenant, environment, id)
	lines := remotesession.RemoteAppSessionMasterScanLines(socket)
	lines = append(lines,
		"if [ -n \"$master_pid\" ]; then kill \"$master_pid\" 2>/dev/null || true; fi",
		fmt.Sprintf("rm -f \"%s\" \"%s\"", socket, strings.TrimSuffix(socket, ".dtach")+".owner"),
	)
	return strings.Join(lines, "\n")
}
