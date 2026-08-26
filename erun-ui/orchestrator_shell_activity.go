package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// A background shell an orchestrator starts (Bash with run_in_background) can
// legitimately keep running after the turn that started it ends — that is the
// whole point of backgrounding it. orchestratorActivity (this orchestrator's
// own turn) cannot answer "is a shell still running" for that reason: the turn
// can read idle while a shell it started keeps going, and the desktop had no
// affordance at all for that case.
//
// Like orchestratorActivity, this is a report the agent's hooks write, not
// something read off its terminal — the same reason applies here: a
// background shell that finishes leaves nothing in the terminal for the
// desktop to notice on its own, and scanning rendered text for a specific
// phrase would be exactly the output-driven latch orchestratorActivity's
// header explains was removed and must not return.
//
// A Bash call that backgrounds a shell reports a task id in its own
// tool_response (backgroundTaskId); checking on it later (TaskOutput) or
// killing it (TaskStop) reports the same id back with a status. Two hooks,
// installed on PostToolUse only (tool_response does not exist before a call
// completes), turn that into the report: one matched to "Bash" starts it, one
// matched to "TaskOutput|TaskStop" clears it when the id it names matches what
// is currently recorded and the reported status is no longer "running".
//
// Node, not a bare shell redirect, does the parsing: the report needs actual
// fields out of the hook's JSON stdin (the command, the background task id,
// the polled status), which a printf-only one-liner cannot read at all. Node
// is guaranteed present without depending on erun being on PATH — the
// orchestrator session could not have started without it, since the AI
// harness invoked to launch it is itself an npm package.
type orchestratorShellActivity struct {
	Running bool   `json:"running"`
	Command string `json:"command,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
	// SessionID is the id of the session that wrote this report, taken from the
	// hook's own stdin. An orchestrator id is reused across restarts, so this is
	// what lets
	// a later read tell a report apart from one a since-replaced session left
	// behind, instead of trusting "running" from whichever session happens to be
	// live for the id now.
	SessionID string `json:"sessionId,omitempty"`
	AtUnix    int64  `json:"atUnix"`
}

// orchestratorShellActivitySafetyBound is the outer bound on a "running"
// report from a session the desktop can still see. It is generous — hours,
// not minutes — because a legitimate background shell (a build, a long poll
// loop) can run that long, and showing the operator its real elapsed time
// rather than hiding it past some short cutoff is the point of this report.
// It exists only so a shell ended by some path neither hook observes (a bare
// `kill` run from inside the shell itself, an orchestrator that exits without
// ever checking on it) cannot pin the indicator on forever; the explicit clear
// hook is expected to beat it in the ordinary case. A session the desktop can
// no longer see ages out on the short orchestratorActivityTTL instead, the
// same asymmetry readOrchestratorActivity applies and for the same reason:
// nothing will ever explicitly clear a report for a session that is gone.
const orchestratorShellActivitySafetyBound = 6 * time.Hour

// orchestratorShellActivityDir is the directory the reports live in, a
// sibling of orchestrator-activity.
func orchestratorShellActivityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-shell-activity")
}

// orchestratorShellActivityPath is the file one orchestrator's hooks write.
func orchestratorShellActivityPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return filepath.Join(orchestratorShellActivityDir(), id+".json")
}

// readOrchestratorShellActivity reports whether this orchestrator says a
// background shell it started is still running. sessionAlive picks the
// staleness bound: a report from a session the desktop can no longer see ages
// out on the short, shared bound, because nothing will ever explicitly clear
// it for a session that is gone.
//
// An orchestrator id is reused by design on every restart, and sessionAlive is
// computed per id — from whichever session is live for that id right now —
// not per the session that actually wrote this report. So a "running" report
// left behind by a session that has since been replaced would otherwise be
// protected by its successor's liveness and get the generous bound forever,
// which is #1274. liveSessionID is the id the desktop currently has recorded
// as live for this orchestrator; a report naming a different one names a
// session that is no longer live for real, and is rejected regardless of the
// staleness bound. Neither an empty report session id (an older write, or a
// clear which never carries one) nor an empty liveSessionID (no live-session
// record yet) is treated as a mismatch — there is nothing to compare, and
// rejecting on that basis alone would make every report unusable before the
// live-session recorder has ever fired.
func readOrchestratorShellActivity(id string, now time.Time, sessionAlive bool, liveSessionID string) (orchestratorShellActivity, bool) {
	path := orchestratorShellActivityPath(id)
	if path == "" {
		return orchestratorShellActivity{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorShellActivity{}, false
	}
	var activity orchestratorShellActivity
	if err := json.Unmarshal(data, &activity); err != nil {
		return orchestratorShellActivity{}, false
	}
	if activity.Running && shellActivityNamesAReplacedSession(activity.SessionID, liveSessionID) {
		return orchestratorShellActivity{}, false
	}
	bound := orchestratorActivityTTL
	if sessionAlive {
		bound = orchestratorShellActivitySafetyBound
	}
	if activity.AtUnix <= 0 || now.Sub(time.Unix(activity.AtUnix, 0)) > bound {
		return orchestratorShellActivity{}, false
	}
	return activity, true
}

// shellActivityNamesAReplacedSession reports whether a report's own session id
// names a session other than the one currently recorded as live. Either side
// being empty means there is nothing to compare, not a mismatch: an older
// report never carried a session id at all, and a fresh orchestrator has no
// live-session record until its recorder hook first fires.
func shellActivityNamesAReplacedSession(reportSessionID, liveSessionID string) bool {
	return reportSessionID != "" && liveSessionID != "" && reportSessionID != liveSessionID
}

// orchestratorShellActivityFileVar is both the environment variable the hook
// commands resolve their report path through and the marker
// isOrchestratorShellActivityHookBlock looks for, so a rewrite recognizes and
// replaces its own previous blocks instead of stacking another copy beside
// them.
const orchestratorShellActivityFileVar = "ERUN_SHELL_ACTIVITY_FILE"

// isOrchestratorShellActivityHookBlock reports whether a settings hook block
// is one of ours — either the start or the clear block, which share the
// marker.
func isOrchestratorShellActivityHookBlock(block any) bool {
	group, ok := block.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		command, ok := entry["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(command, orchestratorShellActivityFileVar) {
			return true
		}
	}
	return false
}

// orchestratorShellActivityStartHookCommand recognizes a Bash call that
// backgrounded a shell (tool_response.backgroundTaskId) and records it as
// running, with the command it is running, the task id later needed to clear
// it, and the id of the session that started it (the top-level session_id on
// the hook's stdin) — what later lets a stale report be told apart from one
// whose session is
// still live. Any other Bash call — foreground, or one that never
// backgrounds — leaves the previous report untouched: a foreground command
// says nothing about whatever shell is already running.
func orchestratorShellActivityStartHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const j=JSON.parse(d);` +
		`const id=j.tool_response&&j.tool_response.backgroundTaskId;` +
		`if(!id)return;` +
		`const out={running:true,command:(j.tool_input&&j.tool_input.command)||"",taskId:id,sessionId:j.session_id||"",atUnix:Math.floor(Date.now()/1000)};` +
		`require("fs").writeFileSync(process.env.` + orchestratorShellActivityFileVar + `,JSON.stringify(out));` +
		`}catch(e){}});`
	return `[ -n "$ERUN_ORCHESTRATOR_ID" ] && mkdir -p "` + dir + `" && ` +
		orchestratorShellActivityFileVar + `="` + dir + `/$ERUN_ORCHESTRATOR_ID.json" node -e '` + script + `' || true`
}

// orchestratorShellActivityClearHookCommand recognizes a TaskOutput or
// TaskStop call that reports the SAME task id the start hook recorded as no
// longer running, and clears the report. A call naming a different task id
// (the orchestrator is watching something else entirely) or one still
// reporting "running" leaves the report untouched.
func orchestratorShellActivityClearHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const j=JSON.parse(d);` +
		`const task=(j.tool_response&&j.tool_response.task)||{};` +
		`const id=task.task_id||(j.tool_input&&(j.tool_input.task_id||j.tool_input.shell_id))||"";` +
		`if(!id||task.status==="running")return;` +
		`const path=process.env.` + orchestratorShellActivityFileVar + `;` +
		`let prev={};` +
		`try{prev=JSON.parse(require("fs").readFileSync(path,"utf8"));}catch(e){}` +
		`if(prev.taskId!==id)return;` +
		`require("fs").writeFileSync(path,JSON.stringify({running:false,atUnix:Math.floor(Date.now()/1000)}));` +
		`}catch(e){}});`
	return `[ -n "$ERUN_ORCHESTRATOR_ID" ] && mkdir -p "` + dir + `" && ` +
		orchestratorShellActivityFileVar + `="` + dir + `/$ERUN_ORCHESTRATOR_ID.json" node -e '` + script + `' || true`
}

// orchestratorShellActivityResetHookCommand clears an inherited "running"
// report at a session boundary, the same reason orchestratorActivityHookCommand
// (false) clears the busy report there: an orchestrator id is mutable and
// reusable, so a report a previous run under this id left behind — including
// one whose shell finished without either hook ever observing it — must not
// survive into a session that has not started any shell of its own yet. A
// bare printf, like the busy report's reset, since there is nothing to parse:
// it always writes the same fixed shape.
func orchestratorShellActivityResetHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	return `[ -n "$ERUN_ORCHESTRATOR_ID" ] && mkdir -p "` + dir + `" && ` +
		`printf '{"running":false,"atUnix":%s}' "$(date +%s)" > "` + dir + `/$ERUN_ORCHESTRATOR_ID.json"` +
		` || true`
}

// orchestratorShellActivityHookBlocks are the two PostToolUse-only hooks the
// settings file installs, each matcher-scoped so it fires only for the calls
// it needs to see rather than every tool call.
func orchestratorShellActivityHookBlocks() []any {
	return []any{
		map[string]any{
			"matcher": "Bash",
			"hooks":   []any{map[string]any{"type": "command", "command": orchestratorShellActivityStartHookCommand()}},
		},
		map[string]any{
			"matcher": "TaskOutput|TaskStop",
			"hooks":   []any{map[string]any{"type": "command", "command": orchestratorShellActivityClearHookCommand()}},
		},
	}
}
