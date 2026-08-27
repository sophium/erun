package eruncommon

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
)

// The environment half of the whip pass (erun#1379) runs where its target
// lives: `emcp` always runs inside the environment's own runtime pod (see
// erun-mcp/AGENTS.md), so a tool call against one environment's own edge is
// already executing on the same filesystem as that environment's dtach
// sessions. Pushing a nudge is therefore a local exec, not a kubectl-exec — no
// separate remote-command plumbing is needed, and every transport (CLI, MCP,
// desktop) reaches the same environment the same way: call its "whip" tool.

// WhipEnvironmentAgentSessionID matches the AppSession id erun open's AI tab
// creates (see erun-common/open.go, erun-ui/terminal_sessions.go), so a whip
// nudge always targets the same dtach session an operator's "AI" tab reattaches
// to.
const WhipEnvironmentAgentSessionID = "ai"

// WhipCommandRunner runs one local command to completion and returns its
// combined output. Injected so nudging is testable without a real dtach.
type WhipCommandRunner func(name string, args ...string) ([]byte, error)

// RunWhipCommand is the production WhipCommandRunner.
func RunWhipCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

// whipNudgeSleep is time.Sleep, swappable in tests so the settle write order
// can be asserted without actually waiting.
var whipNudgeSleep = time.Sleep

// environmentWhipSessionAlive reports whether the environment's AI dtach
// session currently has a live program behind it, the same /proc-based scan
// RemoteAppSessionHeartbeatScript uses, run locally rather than over kubectl
// exec.
func environmentWhipSessionAlive(runner WhipCommandRunner, tenant, environment string) (bool, error) {
	socket := remoteAppSessionSocketPath(tenant, environment, WhipEnvironmentAgentSessionID)
	script := strings.Join(append(remoteAppSessionMasterScanLines(socket), `printf '%s' "$master_pid"`), "\n")
	out, err := runner("sh", "-c", script)
	if err != nil {
		return false, fmt.Errorf("whip: probing environment session liveness: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// pushEnvironmentWhipNudge writes the whip text into the environment's live AI
// session, then — after settle — a bare carriage return to submit it: the same
// two-write shape writeOrchestratorPacingNudge uses so the pty's far side
// cannot coalesce them into one read. dtach has no non-interactive "send text"
// mode, so each write attaches (-a) with the text piped on stdin and detaches
// again on EOF; `timeout` bounds the attach in case the far side never drains
// it. `-r none` skips dtach's own redraw trigger, since this is a push, not an
// operator reattaching.
func pushEnvironmentWhipNudge(runner WhipCommandRunner, tenant, environment, text string, settle time.Duration) error {
	socket := shellSingleQuote(remoteAppSessionSocketPath(tenant, environment, WhipEnvironmentAgentSessionID))

	textScript := fmt.Sprintf("printf %%s %s | timeout 5 dtach -a %s -r none", shellSingleQuote(text), socket)
	if _, err := runner("sh", "-c", textScript); err != nil {
		return fmt.Errorf("whip: writing nudge text: %w", err)
	}
	whipNudgeSleep(settle)
	// The submitting carriage return goes through printf's own format-string
	// escape handling (no %s) so it lands as one real CR byte, not the two
	// literal characters `\` and `r` a %s substitution would produce.
	crScript := fmt.Sprintf(`printf '\r' | timeout 5 dtach -a %s -r none`, socket)
	if _, err := runner("sh", "-c", crScript); err != nil {
		return fmt.Errorf("whip: submitting nudge with carriage return: %w", err)
	}
	return nil
}

// shellSingleQuote makes s safe to embed as a single sh word, so an operator's
// configured message can contain anything (quotes, `$`, backticks) without
// escaping into the shell it is piped through.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// whipEnvironmentState is the environment agent's own consecutive-nudge
// bookkeeping, the local-filesystem equivalent of the in-memory
// pacingNudgeCount/pacingCapped fields an orchestrator's desktop-held session
// carries. Persisted so it survives an `emcp` restart inside the pod.
type whipEnvironmentState struct {
	NudgeCount      int   `json:"nudgeCount"`
	Capped          bool  `json:"capped"`
	LastNudgeAtUnix int64 `json:"lastNudgeAtUnix,omitempty"`
}

func whipEnvironmentStatePath(tenant, environment string) (string, error) {
	return xdg.CacheFile(filepath.Join("erun", "whip", sanitizeForFilename(tenant), sanitizeForFilename(environment), "state.json"))
}

func loadWhipEnvironmentState(tenant, environment string) (whipEnvironmentState, bool) {
	path, err := whipEnvironmentStatePath(tenant, environment)
	if err != nil {
		return whipEnvironmentState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return whipEnvironmentState{}, false
	}
	var state whipEnvironmentState
	if err := json.Unmarshal(data, &state); err != nil {
		return whipEnvironmentState{}, false
	}
	return state, true
}

func saveWhipEnvironmentState(tenant, environment string, state whipEnvironmentState) error {
	path, err := whipEnvironmentStatePath(tenant, environment)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// latestEnvironmentAgentActivity reads the environment's own locally recorded
// activity for the markers that name an AI session at work (see
// EnvironmentActivityMarkerDetail), so a rearm decision reuses the same
// activity records the idle/auto-stop machinery already writes rather than a
// second, whip-only probe.
func latestEnvironmentAgentActivity(tenant, environment string) time.Time {
	activity, err := LoadEnvironmentActivity(tenant, environment)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, kind := range []string{ActivityKindCodex, ActivityKindProcess} {
		if snapshot, ok := activity[kind]; ok && snapshot.LastActivity.After(latest) {
			latest = snapshot.LastActivity
		}
	}
	return latest
}

// RunLocalEnvironmentWhip is what the "whip" MCP tool calls on itself: probe
// the environment's own AI session for liveness, decide against its persisted
// nudge bookkeeping (rearmed on fresh locally-recorded AI activity, the same
// rearm rule orchestrator_pacing.go applies to a fresh busy report), and push
// or cap accordingly. It only supports an explicit, operator-triggered call
// today — callers pass explicit=true; there is no automatic schedule-driven
// pass for environments yet, so LastActiveAt is informational only and never
// gates a decision.
func RunLocalEnvironmentWhip(now time.Time, runner WhipCommandRunner, tenant, environment string, cfg WhipConfig, explicit, dryRun bool) (WhipResult, error) {
	id := tenant + "/" + environment
	alive, err := environmentWhipSessionAlive(runner, tenant, environment)
	if err != nil {
		return WhipResult{}, err
	}

	state, _ := loadWhipEnvironmentState(tenant, environment)
	lastActive := latestEnvironmentAgentActivity(tenant, environment)
	if lastActive.After(time.Unix(state.LastNudgeAtUnix, 0)) {
		state.NudgeCount = 0
		state.Capped = false
	}

	candidate := WhipCandidate{
		Kind:         WhipTargetEnvironment,
		ID:           id,
		Name:         id,
		Reachable:    true,
		Alive:        alive,
		LastActiveAt: lastActive,
		NudgeCount:   state.NudgeCount,
		Capped:       state.Capped,
	}
	decision, reason := DecideWhip(candidate, now, cfg, explicit)
	result := WhipResult{Candidate: candidate, Decision: decision, Reason: reason}
	if dryRun {
		return result, nil
	}

	switch decision {
	case WhipDecisionNudge:
		if err := pushEnvironmentWhipNudge(runner, tenant, environment, cfg.Message, orchestratorPacingNudgeSettleDuration); err != nil {
			result.Error = err.Error()
			return result, nil
		}
		result.Pushed = true
		state.NudgeCount++
		state.LastNudgeAtUnix = now.Unix()
		_ = saveWhipEnvironmentState(tenant, environment, state)
	case WhipDecisionCap:
		state.Capped = true
		_ = saveWhipEnvironmentState(tenant, environment, state)
	case WhipDecisionNone:
	}
	return result, nil
}

// orchestratorPacingNudgeSettleDuration mirrors orchestrator_pacing.go's
// orchestratorPacingNudgeSettle (150ms) without erun-ui importing erun-common
// the other way around: both name the same settle so a nudge write is never
// coalesced into one read, whichever population it targets.
const orchestratorPacingNudgeSettleDuration = 150 * time.Millisecond
