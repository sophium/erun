package eruncommon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A persistent AI session's PTY output volume says nothing about what the tool
// is actually doing: a quiet session can be deep in a single long tool call,
// and a chatty one can be stuck retrying. So this file gives the tool a way to
// say what is true instead of making the desktop guess it from bytes.
//
// The tool writes a small status file into the pod as its own turn boundaries
// happen — the same "the agent knows, so it says so" shape as the desktop
// orchestrator's turn-boundary hooks (erun-ui/orchestrator_activity.go), just
// pod-local instead of desktop-local. Claude Code's hook system is the only
// one wired today (installClaudeAISessionStatusHooks in ai_launch.go); a tool
// with no hook mechanism gets no report, and ResolveAISessionStatuses says so
// plainly (AISessionStateUnknown) rather than inferring a state it was never
// told.

// AISessionState is the operator-facing condition of one AI session.
type AISessionState string

const (
	// AISessionStateBusy means the tool itself reported it is mid-turn: it has
	// received a prompt or is running a tool call and has not yet stopped.
	AISessionStateBusy AISessionState = "busy"
	// AISessionStateIdle means the tool itself reported its turn ended, or its
	// program has exited.
	AISessionStateIdle AISessionState = "idle"
	// AISessionStateAwaitingInput means the tool itself reported it is waiting
	// on the operator — a permission prompt or an idle-on-input notification.
	// This is the state a volume heuristic can never produce, because a session
	// waiting on a human and one that finished are both silent.
	AISessionStateAwaitingInput AISessionState = "awaiting-input"
	// AISessionStateUnknown means no structured report exists for this session:
	// the tool has no hook mechanism wired, or nothing has reported yet. This is
	// the honest answer required whenever the state would otherwise have to be
	// inferred rather than told.
	AISessionStateUnknown AISessionState = "unknown"
)

// AISessionOutcome names how a session's tool process ended. Empty means it
// has not (as far as this observation can tell) ended at all.
type AISessionOutcome string

const (
	AISessionOutcomeNone      AISessionOutcome = ""
	AISessionOutcomeExited    AISessionOutcome = "exited"
	AISessionOutcomeOOMKilled AISessionOutcome = "oom-killed"
)

// AISessionStatus is the transport-neutral status of one AI session running in
// an environment's runtime pod. It is the public contract the CLI, the MCP
// `idle` tool, the desktop, and eventually the console and a native client all
// read the same way.
type AISessionStatus struct {
	// SessionID is the persistent pod session id the AI tool runs in: "ai" or
	// "contribute-ai" today (see erun-common/open.go ShellLaunchParams.AppSession).
	SessionID string `json:"sessionId"`
	// Tool is the AI tool launched in this session ("claude", "codex", or
	// whatever EnvConfig.AITool names). Never blank when the session exists.
	Tool string `json:"tool"`
	// State is never inferred from output volume or timing; see AISessionState.
	State AISessionState `json:"state"`
	// LastActivity is when the state was last confirmed: the tool's own last
	// report, or the process exit time once Outcome is set. Zero when nothing
	// has ever reported (State is then AISessionStateUnknown).
	LastActivity time.Time `json:"lastActivity,omitempty"`
	// Outcome and ExitCode are set once the session's tool process has exited.
	// State is AISessionStateIdle whenever Outcome is set: nothing is running.
	Outcome  AISessionOutcome `json:"outcome,omitempty"`
	ExitCode int              `json:"exitCode,omitempty"`
}

// aiSessionSelfReport is the JSON one hook invocation writes.
type aiSessionSelfReport struct {
	State  AISessionState `json:"state"`
	AtUnix int64          `json:"atUnix"`
}

// aiSessionExitReport is the JSON the launch wrapper's exit trap writes.
type aiSessionExitReport struct {
	Outcome  AISessionOutcome `json:"outcome"`
	ExitCode int              `json:"exitCode"`
	AtUnix   int64            `json:"atUnix"`
}

// RemoteAppSessionStatusDir holds the AI session self-reports and exit
// outcomes, one file pair per persistent session id, beside (but distinct
// from) RemoteAppSessionSocketDir's sockets. Kept out of the socket dir so a
// glob over one never has to skip the other's files.
const RemoteAppSessionStatusDir = "/tmp/erun-sessions-status"

// aiSessionStatusIDs are the persistent session ids that ever run an AI tool.
// "open-N" shells run an interactive login shell, never the managed AI tool
// launch, so they are not candidates here.
var aiSessionStatusIDs = []string{"ai", "contribute-ai"}

func aiSessionStatusFilePrefix(tenant, environment string) string {
	return sanitizeForFilename(tenant) + "-" + sanitizeForFilename(environment) + "-"
}

func aiSessionReportPathIn(dir, tenant, environment, id string) string {
	return filepath.Join(dir, aiSessionStatusFilePrefix(tenant, environment)+sanitizeForFilename(id)+".status.json")
}

func aiSessionExitPathIn(dir, tenant, environment, id string) string {
	return filepath.Join(dir, aiSessionStatusFilePrefix(tenant, environment)+sanitizeForFilename(id)+".exit.json")
}

// AISessionStatusReportCommand is the pod-local shell one-liner a hook runs to
// record its own turn boundary. It is a bare shell redirect, not a call into
// erun, for the same reason orchestratorActivityHookCommand is: this runs on
// every prompt and every tool call of every AI session, and a hook that needed
// erun on PATH would stop reporting exactly when the pod's PATH is broken.
func AISessionStatusReportCommand(tenant, environment, id string, state AISessionState) string {
	return aiSessionStatusReportCommandIn(RemoteAppSessionStatusDir, tenant, environment, id, state)
}

func aiSessionStatusReportCommandIn(dir, tenant, environment, id string, state AISessionState) string {
	path := aiSessionReportPathIn(dir, tenant, environment, id)
	return `mkdir -p "` + dir + `" && printf '{"state":"` + string(state) + `","atUnix":%s}' "$(date +%s)" > "` + path + `" || true`
}

// AISessionExitReportCommand is appended to the launch wrapper's exit trap,
// where $ai_status already holds the tool process's exit code (see
// AISessionLaunchLines). Exit code 137 is SIGKILL (128+9), the signal the
// kernel OOM killer sends — the same code the wrapper's printed banner already
// keys its "likely out of memory" message on.
func AISessionExitReportCommand(tenant, environment, id string) string {
	return aiSessionExitReportCommandIn(RemoteAppSessionStatusDir, tenant, environment, id)
}

func aiSessionExitReportCommandIn(dir, tenant, environment, id string) string {
	path := aiSessionExitPathIn(dir, tenant, environment, id)
	return `mkdir -p "` + dir + `" && ` +
		`ai_outcome=exited; [ "$ai_status" = 137 ] && ai_outcome=oom-killed; ` +
		`printf '{"outcome":"%s","exitCode":%s,"atUnix":%s}' "$ai_outcome" "$ai_status" "$(date +%s)" > "` + path + `" || true`
}

// ResolveAISessionStatuses reads every AI-capable persistent session id's
// self-report and exit outcome for one tenant+environment, purely from the
// pod's local filesystem — this runs inside the pod (the MCP `idle` tool's own
// process), so no exec/kubectl bridge is needed to read what the tool itself
// wrote next to it.
//
// A session with no socket at all is not reported: there is nothing running
// and nothing to say unknown about. A session whose socket exists but has
// never self-reported and never exited is AISessionStateUnknown — the honest
// answer for a tool with no hook mechanism wired (or one that has not reached
// its first turn boundary yet), never a guess derived from silence.
func ResolveAISessionStatuses(tenant, environment, aiTool string) []AISessionStatus {
	return resolveAISessionStatusesIn(RemoteAppSessionSocketDir, RemoteAppSessionStatusDir, tenant, environment, aiTool)
}

func resolveAISessionStatusesIn(socketDir, statusDir, tenant, environment, aiTool string) []AISessionStatus {
	tool := strings.TrimSpace(aiTool)
	if tool == "" {
		tool = defaultAITool
	}
	var out []AISessionStatus
	for _, id := range aiSessionStatusIDs {
		socket := filepath.Join(socketDir, sanitizeForFilename(tenant)+"-"+sanitizeForFilename(environment)+"-"+sanitizeForFilename(id)+".dtach")
		if _, err := os.Stat(socket); err != nil {
			continue
		}
		out = append(out, resolveOneAISessionStatus(statusDir, tenant, environment, id, tool))
	}
	return out
}

func resolveOneAISessionStatus(statusDir, tenant, environment, id, tool string) AISessionStatus {
	status := AISessionStatus{SessionID: id, Tool: tool, State: AISessionStateUnknown}

	if exit, ok := readAISessionExitReport(statusDir, tenant, environment, id); ok {
		status.State = AISessionStateIdle
		status.Outcome = exit.Outcome
		status.ExitCode = exit.ExitCode
		status.LastActivity = time.Unix(exit.AtUnix, 0)
		return status
	}

	if report, ok := readAISessionSelfReport(statusDir, tenant, environment, id); ok {
		status.State = report.State
		status.LastActivity = time.Unix(report.AtUnix, 0)
	}
	return status
}

func readAISessionSelfReport(dir, tenant, environment, id string) (aiSessionSelfReport, bool) {
	data, err := os.ReadFile(aiSessionReportPathIn(dir, tenant, environment, id))
	if err != nil {
		return aiSessionSelfReport{}, false
	}
	var report aiSessionSelfReport
	if err := json.Unmarshal(data, &report); err != nil || report.AtUnix <= 0 {
		return aiSessionSelfReport{}, false
	}
	switch report.State {
	case AISessionStateBusy, AISessionStateIdle, AISessionStateAwaitingInput:
	default:
		return aiSessionSelfReport{}, false
	}
	return report, true
}

func readAISessionExitReport(dir, tenant, environment, id string) (aiSessionExitReport, bool) {
	data, err := os.ReadFile(aiSessionExitPathIn(dir, tenant, environment, id))
	if err != nil {
		return aiSessionExitReport{}, false
	}
	var exit aiSessionExitReport
	if err := json.Unmarshal(data, &exit); err != nil || exit.AtUnix <= 0 {
		return aiSessionExitReport{}, false
	}
	if exit.Outcome != AISessionOutcomeExited && exit.Outcome != AISessionOutcomeOOMKilled {
		return aiSessionExitReport{}, false
	}
	return exit, true
}
