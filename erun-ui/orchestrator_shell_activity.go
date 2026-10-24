package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// orchestratorShellActivity is a hook-written report of a background shell that
// can outlive its turn. Hooks run on PostToolUse only, since tool_response does
// not exist earlier: Bash starts it, TaskOutput|TaskStop clears a matching id.
// Node parses the hook JSON and is always present, since the harness is an npm package.
type orchestratorShellActivity struct {
	Running bool   `json:"running"`
	Command string `json:"command,omitempty"`
	TaskID  string `json:"taskId,omitempty"`
	// SessionID distinguishes a report from one a replaced session left under a reused id.
	SessionID string `json:"sessionId,omitempty"`
	AtUnix    int64  `json:"atUnix"`
}

// orchestratorShellActivitySafetyBound only stops an unobserved exit from pinning
// the indicator forever; real shells can run for hours. Sessions no longer seen
// age out on orchestratorActivityTTL instead.
const orchestratorShellActivitySafetyBound = 6 * time.Hour

func orchestratorShellActivityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-shell-activity")
}

func orchestratorShellActivityPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	return filepath.Join(orchestratorShellActivityDir(), id+".json")
}

// readOrchestratorShellActivity applies the short TTL when !sessionAlive, and
// rejects a report from a session other than liveSessionID, since ids are reused.
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

// shellActivityNamesAReplacedSession treats an empty id on either side as no mismatch.
func shellActivityNamesAReplacedSession(reportSessionID, liveSessionID string) bool {
	return reportSessionID != "" && liveSessionID != "" && reportSessionID != liveSessionID
}

// orchestratorShellActivityHookMarker is the report directory baked into each command.
func orchestratorShellActivityHookMarker() string {
	return filepath.ToSlash(orchestratorShellActivityDir())
}

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

// orchestratorShellActivityStartHookCommand records a backgrounded Bash call's
// task id, command, and session id; other Bash calls leave the report alone.
// Everything runs inside node because PowerShell parses no POSIX shell prefix.
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

// orchestratorShellActivityClearHookCommand clears only on the recorded task id
// with a non-running status.
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

// orchestratorShellActivityResetHookCommand clears an inherited report at a
// session boundary, since ids are reused.
func orchestratorShellActivityResetHookCommand() string {
	dir := filepath.ToSlash(orchestratorShellActivityDir())
	script := `try{const id=process.env.ERUN_ORCHESTRATOR_ID;if(id){const fs=require("fs");` +
		`fs.mkdirSync("` + dir + `",{recursive:true});` +
		`fs.writeFileSync("` + dir + `/"+id+".json",JSON.stringify({running:false,atUnix:Math.floor(Date.now()/1000)}));` +
		`}}catch(e){}`
	return `node -e '` + script + `'`
}

// reconcileOrphanedOrchestratorShellActivity deletes stale reports for ids with no
// live session. Transient ids are never read again, so without this sweep an
// uncleared report would persist forever.
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

// aliveOrchestratorIDs includes transient sessions, which keep the long bound while running.
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
