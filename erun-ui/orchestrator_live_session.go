package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// A restart hand-off names the conversation to resume by id. The id erun
// SPAWNED the session with is not reliably that conversation: Claude Code
// forks the transcript to a NEW session id when a resumed conversation
// compacts, so after any compaction the spawn-time id names a conversation
// that has stopped growing. The forked transcript carries no structural
// parent pointer back to the id it forked from, so there is no reading it
// off disk after the fact — the only reliable source is the live session
// telling us its own id as it goes, which is exactly what a hook's stdin JSON
// carries on every invocation.
//
// The hooks installed by ensureOrchestratorSessionStartHook keep one file per
// orchestrator current with that id: reset to the spawn id at SessionStart
// (startup/resume), then refreshed from every turn-boundary hook afterward, so
// a fork is reflected the moment the next hook fires. Resetting at
// SessionStart matters because an orchestrator id is mutable and reusable —
// without the reset, a leftover record from a previous run under the same id
// would outlive it.

// orchestratorLiveSessionDirName holds one file per orchestrator recording the
// session id its own hooks last saw live.
const orchestratorLiveSessionDirName = "orchestrator-session"

// orchestratorLiveSession is what one orchestrator's file carries.
type orchestratorLiveSession struct {
	SessionID string `json:"sessionId"`
	AtUnix    int64  `json:"atUnix,omitempty"`
}

// orchestratorLiveSessionDir is the directory the hooks write into and the
// desktop reads from, beside the other per-orchestrator state under
// UserConfigDir()/ERun.
func orchestratorLiveSessionDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", orchestratorLiveSessionDirName)
}

// orchestratorLiveSessionPath is one orchestrator's own file.
func orchestratorLiveSessionPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return filepath.Join(orchestratorLiveSessionDir(), id+".json")
}

// readOrchestratorLiveSessionID reads the session id an orchestrator's own
// hooks last recorded as live. Unreadable, unparseable, or blank all answer
// false: the caller falls back to the id it already trusts rather than acting
// on a record that cannot be trusted.
func readOrchestratorLiveSessionID(id string) (string, bool) {
	path := orchestratorLiveSessionPath(id)
	if path == "" {
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	var record orchestratorLiveSession
	if err := json.Unmarshal(data, &record); err != nil {
		return "", false
	}
	record.SessionID = strings.TrimSpace(record.SessionID)
	if record.SessionID == "" {
		return "", false
	}
	return record.SessionID, true
}

// preferLiveOrchestratorSessionID returns the session id an orchestrator's own
// hooks last recorded as live, falling back to the id it was spawned with when
// the record is absent, names the same id (nothing to prefer), or names a
// conversation that no longer exists on disk. It never trusts a live record
// over a spawn-time id it cannot verify: resuming nothing is safer than
// resuming the wrong conversation.
func preferLiveOrchestratorSessionID(orchestratorID, spawnConversationID string) string {
	live, ok := readOrchestratorLiveSessionID(orchestratorID)
	if !ok || live == "" || live == spawnConversationID {
		return spawnConversationID
	}
	if !orchestratorSessionExists(live) {
		return spawnConversationID
	}
	return live
}

// orchestratorSessionRecordHookCommand is the hook command that keeps one
// orchestrator's live-session file current. It reads the hook's own stdin JSON
// for session_id — the same field every Claude Code hook invocation carries,
// which Claude Code updates the moment it forks the transcript on compaction —
// and writes it under $ERUN_ORCHESTRATOR_ID, resolved at run time for the same
// reason the activity hooks resolve it at run time: the orchestrators
// workspace is shared, so a baked-in id would have every orchestrator
// overwriting the same file.
//
// Bare shell, like the activity hooks and the no-ask guard, so it keeps
// working when erun is not on PATH. Every failure path is `|| true`: a hook
// that could wedge a session costs more than a missed record.
func orchestratorSessionRecordHookCommand() string {
	dir := filepath.ToSlash(orchestratorLiveSessionDir())
	return `input=$(cat); ` +
		`sid=$(printf '%s' "$input" | sed -n 's/.*"session_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'); ` +
		`[ -n "$ERUN_ORCHESTRATOR_ID" ] && [ -n "$sid" ] && mkdir -p "` + dir + `" && ` +
		`printf '{"sessionId":"%s","atUnix":%s}' "$sid" "$(date +%s)" > "` + dir + `/$ERUN_ORCHESTRATOR_ID.json"` +
		` || true`
}

// orchestratorLiveSessionHookMarker identifies a hook this file wrote, so a
// rewrite replaces its own previous block instead of stacking another copy,
// and merging into an event leaves everyone else's hooks alone.
func orchestratorLiveSessionHookMarker() string {
	return filepath.ToSlash(orchestratorLiveSessionDir())
}

// isOrchestratorLiveSessionHookBlock reports whether a settings hook block is
// one of ours. Anything it cannot read is somebody else's and is kept.
func isOrchestratorLiveSessionHookBlock(block any) bool {
	group, ok := block.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	marker := orchestratorLiveSessionHookMarker()
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		command, ok := entry["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(command, marker) && strings.Contains(command, `"sessionId":`) {
			return true
		}
	}
	return false
}

// orchestratorLiveSessionHookBlock is the recorder bound to whichever event it
// is installed on.
func orchestratorLiveSessionHookBlock() []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorSessionRecordHookCommand()}},
	}}
}
