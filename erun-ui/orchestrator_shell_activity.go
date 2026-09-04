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

// orchestratorShellActivityHookMarker identifies a hook this file wrote, so a
// rewrite replaces its own previous block instead of stacking another copy
// beside it. The directory every one of these hooks resolves its report path
// under is baked into the command at generation time and unique to this
// report, so it doubles as the marker -- the same approach
// orchestratorActivityHookMarker uses.
func orchestratorShellActivityHookMarker() string {
	return filepath.ToSlash(orchestratorShellActivityDir())
}

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
	marker := orchestratorShellActivityHookMarker()
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		command, ok := entry["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(command, marker) {
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
//
// Runs entirely inside the node script, including the orchestrator-id guard
// and the report directory's creation: a POSIX shell prefix ("[ -n ... ] &&
// mkdir ... && VAR=... node -e ...") is exactly the syntax Windows' own hook
// shell (PowerShell) does not parse the way a POSIX shell does, and
// "VAR=value command" inline env assignment is not PowerShell syntax at all.
func orchestratorShellActivityStartHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const id=process.env.ERUN_ORCHESTRATOR_ID;if(!id)return;` +
		`const j=JSON.parse(d);` +
		`const taskId=j.tool_response&&j.tool_response.backgroundTaskId;` +
		`if(!taskId)return;` +
		`const out={running:true,command:(j.tool_input&&j.tool_input.command)||"",taskId:taskId,sessionId:j.session_id||"",atUnix:Math.floor(Date.now()/1000)};` +
		`const fs=require("fs");fs.mkdirSync("` + dir + `",{recursive:true});` +
		`fs.writeFileSync("` + dir + `/"+id+".json",JSON.stringify(out));` +
		`}catch(e){}});`
	return `node -e '` + script + `'`
}

// orchestratorShellActivityClearHookCommand recognizes a TaskOutput or
// TaskStop call that reports the SAME task id the start hook recorded as no
// longer running, and clears the report. A call naming a different task id
// (the orchestrator is watching something else entirely) or one still
// reporting "running" leaves the report untouched.
func orchestratorShellActivityClearHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const id=process.env.ERUN_ORCHESTRATOR_ID;if(!id)return;` +
		`const j=JSON.parse(d);` +
		`const task=(j.tool_response&&j.tool_response.task)||{};` +
		`const taskId=task.task_id||(j.tool_input&&(j.tool_input.task_id||j.tool_input.shell_id))||"";` +
		`if(!taskId||task.status==="running")return;` +
		`const fs=require("fs");fs.mkdirSync("` + dir + `",{recursive:true});` +
		`const path="` + dir + `/"+id+".json";` +
		`let prev={};` +
		`try{prev=JSON.parse(fs.readFileSync(path,"utf8"));}catch(e){}` +
		`if(prev.taskId!==taskId)return;` +
		`fs.writeFileSync(path,JSON.stringify({running:false,atUnix:Math.floor(Date.now()/1000)}));` +
		`}catch(e){}});`
	return `node -e '` + script + `'`
}

// orchestratorShellActivityResetHookCommand clears an inherited "running"
// report at a session boundary, the same reason orchestratorActivityHookCommand
// (false) clears the busy report there: an orchestrator id is mutable and
// reusable, so a report a previous run under this id left behind — including
// one whose shell finished without either hook ever observing it — must not
// survive into a session that has not started any shell of its own yet.
func orchestratorShellActivityResetHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `try{const id=process.env.ERUN_ORCHESTRATOR_ID;if(id){const fs=require("fs");` +
		`fs.mkdirSync("` + dir + `",{recursive:true});` +
		`fs.writeFileSync("` + dir + `/"+id+".json",JSON.stringify({running:false,atUnix:Math.floor(Date.now()/1000)}));` +
		`}}catch(e){}`
	return `node -e '` + script + `'`
}

// reconcileOrphanedOrchestratorShellActivity deletes on-disk reports for
// orchestrator ids the desktop is not currently holding a live session for,
// once they are stale enough that readOrchestratorShellActivity would have
// discarded them anyway had anyone asked.
//
// Nobody ever asks for a transient id (investigate.go, report_bug.go): each
// one is minted from a nanosecond timestamp and used exactly once, so once its
// session ends no future read will ever name that id again —
// reconcileOrchestratorActivity only visits ids currently in a.orchestrators,
// and skips transient ones outright even while they are alive. Without this
// sweep, a "running: true" report a session never got to clear (because it
// ended without ever checking TaskOutput/TaskStop on its own backgrounded
// shell) sits on disk asserting that forever, since nothing else will ever
// read, correct, or delete it (erun#2144). Sweeping the directory itself,
// rather than only reading on request, is what actually reaps it: the bound
// applied is exactly the one readOrchestratorShellActivity already uses for a
// session it can no longer see, just run unconditionally instead of only for
// an id someone happens to still be watching. This also covers a session
// killed outright rather than cleanly stopped — the sweep needs no exit hook
// to have run, only the file's own age.
func (a *App) reconcileOrphanedOrchestratorShellActivity(now time.Time) {
	dir := orchestratorShellActivityDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	alive := a.aliveOrchestratorIDs()
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		id := strings.TrimSuffix(name, ".json")
		if id == name {
			continue
		}
		bound := orchestratorActivityTTL
		if _, ok := alive[id]; ok {
			bound = orchestratorShellActivitySafetyBound
		}
		path := filepath.Join(dir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var report orchestratorShellActivity
		if err := json.Unmarshal(data, &report); err != nil || report.AtUnix <= 0 {
			continue
		}
		if now.Sub(time.Unix(report.AtUnix, 0)) <= bound {
			continue
		}
		_ = os.Remove(path)
	}
}

// aliveOrchestratorIDs is every orchestrator id the desktop currently holds a
// live session for, transient or not. The sweep above needs "is anyone still
// able to write this id's next report", not "is this a named, persisted
// orchestrator" — a transient (investigate/report-bug) session still gets the
// generous safety bound while it is genuinely running, and drops to the short
// one the moment it ends, exactly like a named orchestrator's session does.
func (a *App) aliveOrchestratorIDs() map[string]struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make(map[string]struct{}, len(a.orchestrators))
	for id, session := range a.orchestrators {
		if session == nil {
			continue
		}
		if managed := a.sessions[orchestratorSessionKey(id)]; managed != nil && !managed.closed {
			ids[id] = struct{}{}
		}
	}
	return ids
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
