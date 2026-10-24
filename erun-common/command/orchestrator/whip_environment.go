package orchestrator

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	activitypkg "github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/open/appsession"
)

// emcp runs inside the environment's pod, so pushing a nudge is a local dtach exec.

// WhipEnvironmentAgentSessionID is the dtach session the "AI" tab reattaches to.
const WhipEnvironmentAgentSessionID = "ai"

// WhipCommandRunner runs one local command and returns its combined output.
type WhipCommandRunner func(name string, args ...string) ([]byte, error)

func RunWhipCommand(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

var whipNudgeSleep = time.Sleep

// environmentWhipSessionAlive runs RemoteAppSessionHeartbeatScript's /proc scan locally.
func environmentWhipSessionAlive(runner WhipCommandRunner, tenant, environment string) (bool, error) {
	socket := appsession.RemoteAppSessionSocketPath(tenant, environment, WhipEnvironmentAgentSessionID)
	script := strings.Join(append(activitypkg.RemoteAppSessionMasterScanLines(socket), `printf '%s' "$master_pid"`), "\n")
	out, err := runner("sh", "-c", script)
	if err != nil {
		return false, fmt.Errorf("whip: probing environment session liveness: %w", err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// pushEnvironmentWhipNudge writes the text, then after settle a CR, so the pty cannot coalesce
// them. dtach has no "send text" mode, so each write attaches with stdin and detaches on EOF;
// `timeout` bounds an attach the far side never drains.
func pushEnvironmentWhipNudge(runner WhipCommandRunner, tenant, environment, text string, settle time.Duration) error {
	socket := shellSingleQuote(appsession.RemoteAppSessionSocketPath(tenant, environment, WhipEnvironmentAgentSessionID))

	textScript := fmt.Sprintf("printf %%s %s | timeout 5 dtach -a %s -r none", shellSingleQuote(text), socket)
	if _, err := runner("sh", "-c", textScript); err != nil {
		return fmt.Errorf("whip: writing nudge text: %w", err)
	}
	whipNudgeSleep(settle)
	// printf's own escape handling yields one real CR byte.
	crScript := fmt.Sprintf(`printf '\r' | timeout 5 dtach -a %s -r none`, socket)
	if _, err := runner("sh", "-c", crScript); err != nil {
		return fmt.Errorf("whip: submitting nudge with carriage return: %w", err)
	}
	return nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// whipEnvironmentState is the agent's nudge bookkeeping, persisted to survive an emcp restart.
type whipEnvironmentState struct {
	NudgeCount      int   `json:"nudgeCount"`
	Capped          bool  `json:"capped"`
	LastNudgeAtUnix int64 `json:"lastNudgeAtUnix,omitempty"`
}

func whipEnvironmentStatePath(tenant, environment string) (string, error) {
	return config.ResolveCacheFilePath(filepath.Join("erun", "whip", core.SanitizeForFilename(tenant), core.SanitizeForFilename(environment), "state.json"))
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

// latestEnvironmentAgentActivity reuses the idle machinery's AI activity markers for rearming.
func latestEnvironmentAgentActivity(tenant, environment string) time.Time {
	activity, err := activitypkg.LoadEnvironmentActivity(tenant, environment)
	if err != nil {
		return time.Time{}
	}
	var latest time.Time
	for _, kind := range []string{config.ActivityKindCodex, activitypkg.ActivityKindProcess} {
		if snapshot, ok := activity[kind]; ok && snapshot.LastActivity.After(latest) {
			latest = snapshot.LastActivity
		}
	}
	return latest
}

// RunLocalEnvironmentWhip is the "whip" MCP tool's local pass. Only explicit calls exist
// today, so LastActiveAt never gates a decision.
func RunLocalEnvironmentWhip(now time.Time, runner WhipCommandRunner, tenant, environment string, cfg WhipConfig, explicit, dryRun bool) (model.WhipResult, error) {
	id := tenant + "/" + environment
	alive, err := environmentWhipSessionAlive(runner, tenant, environment)
	if err != nil {
		return model.WhipResult{}, err
	}

	state, _ := loadWhipEnvironmentState(tenant, environment)
	lastActive := latestEnvironmentAgentActivity(tenant, environment)
	if lastActive.After(time.Unix(state.LastNudgeAtUnix, 0)) {
		state.NudgeCount = 0
		state.Capped = false
	}

	candidate := model.WhipCandidate{
		Kind:         model.WhipTargetEnvironment,
		ID:           id,
		Name:         id,
		Reachable:    true,
		Alive:        alive,
		LastActiveAt: lastActive,
		NudgeCount:   state.NudgeCount,
		Capped:       state.Capped,
	}
	decision, reason := DecideWhip(candidate, now, cfg, explicit)
	result := model.WhipResult{Candidate: candidate, Decision: decision, Reason: reason}
	if dryRun {
		return result, nil
	}

	switch decision {
	case model.WhipDecisionNudge:
		if err := pushEnvironmentWhipNudge(runner, tenant, environment, cfg.Message, orchestratorPacingNudgeSettleDuration); err != nil {
			result.Error = err.Error()
		} else {
			result.Pushed = true
			state.NudgeCount++
			state.LastNudgeAtUnix = now.Unix()
			_ = saveWhipEnvironmentState(tenant, environment, state)
		}
	case model.WhipDecisionCap:
		state.Capped = true
		_ = saveWhipEnvironmentState(tenant, environment, state)
	case model.WhipDecisionNone:
	}
	return result, nil
}

// orchestratorPacingNudgeSettleDuration mirrors erun-ui's orchestratorPacingNudgeSettle.
const orchestratorPacingNudgeSettleDuration = 150 * time.Millisecond
