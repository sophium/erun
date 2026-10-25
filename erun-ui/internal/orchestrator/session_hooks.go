package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const orchestratorContractFallback = "You are a host-side erun orchestrator. Read and follow the CLAUDE.md in this directory and the erun-orchestrate skill before doing anything, even a trivial-looking question."

// orchestratorSkillHookCommand prints the shared CLAUDE.md to plain stdout,
// avoiding the 10,000-char additionalContext cap. It runs through node because
// Windows' PowerShell hook shell misparses POSIX test syntax.
func orchestratorSkillHookCommand(dir string) string {
	claudeMd := filepath.ToSlash(filepath.Join(dir, "CLAUDE.md"))
	// The role file is injected after the shared contract, so ordering is the
	// precedence rule. ERUN_ORCHESTRATOR_ID resolves at run time because this
	// settings file is shared; an empty id or missing file is a silent no-op.
	roleDir := filepath.ToSlash(dir)
	script := `try{const fs=require("fs");` +
		`try{process.stdout.write(fs.readFileSync("` + claudeMd + `","utf8"));}` +
		`catch(e){process.stdout.write("` + orchestratorContractFallback + `\n");}` +
		`const id=process.env.ERUN_ORCHESTRATOR_ID;` +
		`if(id){try{process.stdout.write(fs.readFileSync("` + roleDir + `/CLAUDE."+id+".md","utf8"));}catch(e){}}` +
		`}catch(e){}`
	return `node -e '` + script + `'`
}

// orchestratorRoleFileSeed is written once into CLAUDE.<id>.md and never
// rewritten, unlike the shared CLAUDE.md.
const orchestratorRoleFileSeed = `<!--
This file is yours. erun creates it once and never overwrites it, unlike the
shared CLAUDE.md in this directory, which erun rewrites on every launch.

It is injected on every session boundary -- start, resume, clear, and compact --
immediately AFTER the shared contract, so anything here can add to that
contract or override a line of it.

Put this orchestrator's standing role here: what it owns, what it must not do,
how it should report. A role is standing, so it belongs here rather than in a
task note, which is read once and superseded.

Do NOT list this orchestrator's environments here. The shared contract requires
knowing scope from erun's config and never from disk; a list baked in here would
contradict it and go stale the next time the orchestrator's links change.
-->
`

// ensureOrchestratorRoleFile seeds the role file if absent, on every launch.
// The path uses filepath.Join, never a glob, so a hand-edited id cannot widen it.
func ensureOrchestratorRoleFile(dir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	path := filepath.Join(dir, "CLAUDE."+id+".md")
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(orchestratorRoleFileSeed), 0o644)
}

// ensureOrchestratorSessionStartHook merges into the shared settings file without
// clobbering the operator's keys or hook blocks.
func ensureOrchestratorSessionStartHook(dir string) error {
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &settings) // best-effort; the hook is rebuilt regardless
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	hooks["SessionStart"] = mergeOrchestratorHookBlocks(
		hooks["SessionStart"], orchestratorSessionStartHook(dir), isOrchestratorSessionStartHookBlock)
	// An agent TUI repaints continuously, so activity is self-reported. Busy also
	// goes on tool-call events so long turns renew it; merging preserves the
	// operator's own hooks there.
	busyHook, idleHook := orchestratorActivityHooks()
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		hooks[event] = mergeOrchestratorHookBlocks(hooks[event], busyHook, isOrchestratorActivityHookBlock)
	}
	stop := mergeOrchestratorHookBlocks(hooks["Stop"], idleHook, isOrchestratorActivityHookBlock)
	hooks["Stop"] = mergeOrchestratorHookBlocks(stop, orchestratorNoAskStopGuardBlock(), isOrchestratorNoAskStopGuardBlock)
	// Shell start and completion appear only in tool results, so PostToolUse alone.
	hooks["PostToolUse"] = mergeOrchestratorHookBlocks(
		hooks["PostToolUse"], orchestratorShellActivityHookBlocks(), isOrchestratorShellActivityHookBlock)
	// Older builds' recorders keep writing; strip them wherever found.
	for event := range hooks {
		hooks[event] = pruneRetiredSessionRecorderHooks(hooks[event])
	}
	settings["hooks"] = hooks
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}

// orchestratorNoAskGuardMarker lets a rewrite replace its own block.
const orchestratorNoAskGuardMarker = "noask_guard=1"

// orchestratorNoAskStopGuardReason must stay ASCII and apostrophe-free: it sits
// inside a single-quoted shell argument.
const orchestratorNoAskStopGuardReason = "Your closing message hands the operator a decision. " +
	"The orchestrator contract resolves ambiguity itself and carries the task to a verified end, " +
	"so a question is a defect, not caution. Do what you offered, or state it as a decision already taken, " +
	"and finish. If the action is outward-facing, announce it and proceed - a heads-up is not a gate."

// orchestratorNoAskStopGuardCommand refuses a turn ending by handing the operator
// a decision. It fails open, lets an already-nudged turn go, and runs via node
// for Windows. It reads only the last assistant entry, since the transcript
// echoes the guard's own trigger phrases, and strips quoted spans so citing an
// offer does not fire.
func orchestratorNoAskStopGuardCommand() string {
	script := `/*` + orchestratorNoAskGuardMarker + `*/` +
		`let d="";process.stdin.on("data",c=>{d+=c});process.stdin.on("end",()=>{try{` +
		`const j=JSON.parse(d);` +
		`if(j.stop_hook_active===true)return;` +
		`const fs=require("fs");` +
		`const lines=fs.readFileSync(j.transcript_path,"utf8").split("\n");` +
		`const tail=lines.slice(-40);` +
		`let said="";` +
		`for(const line of tail){if(/"type"\s*:\s*"assistant"/.test(line))said=line;}` +
		`let spoken=said;` +
		`try{const c=JSON.parse(said).message.content;` +
		`if(typeof c==="string")spoken=c;` +
		`else if(Array.isArray(c)){let own="";for(const b of c){if(b&&typeof b.text==="string")own+=b.text+"\n";}if(own)spoken=own;}}catch(e){}` +
		`const cited=/\x60\x60\x60[\s\S]*?\x60\x60\x60|\x60[^\x60]*\x60|"[^"]*"|\u201C[^\u201D]*\u201D|(^|[\s(\[{:,])\u0027[^\u0027\n]*\u0027|^[ \t]*>.*$/gm;` +
		`const trigger=/say the word|let me know if|let me know whether|shall i |do you want me to|would you like me to|next action is yours|if you.d like me to|your call/i;` +
		`if(!trigger.test(spoken.replace(cited," ")))return;` +
		`process.stderr.write("` + orchestratorNoAskStopGuardReason + `");` +
		`process.exit(2);` +
		`}catch(e){}});`
	return `node -e '` + script + `'`
}

func orchestratorNoAskStopGuardBlock() []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorNoAskStopGuardCommand()}},
	}}
}

// isOrchestratorNoAskStopGuardBlock keeps anything it cannot read.
func isOrchestratorNoAskStopGuardBlock(block any) bool {
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
		if command, ok := entry["command"].(string); ok && strings.Contains(command, orchestratorNoAskGuardMarker) {
			return true
		}
	}
	return false
}

// clear and compact are included because /clear drops the role layer and
// compaction forks to a new session id.
const orchestratorSessionStartMatcher = "startup|resume|clear|compact"

func orchestratorSessionStartHook(dir string) []any {
	command := map[string]any{"type": "command", "command": orchestratorSkillHookCommand(dir)}
	// A session killed mid-turn never writes idle, so reset it on start.
	idle := map[string]any{"type": "command", "command": orchestratorActivityHookCommand(false)}
	// A previous run's background shell report can outlive it too.
	shellIdle := map[string]any{"type": "command", "command": orchestratorShellActivityResetHookCommand()}
	return []any{map[string]any{
		"matcher": orchestratorSessionStartMatcher,
		"hooks":   []any{command, idle, shellIdle},
	}}
}

// isOrchestratorSessionStartHookBlock matches our blocks, including the legacy
// per-source pair, by the unique fallback text.
func isOrchestratorSessionStartHookBlock(block any) bool {
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
		if command, ok := entry["command"].(string); ok && strings.Contains(command, orchestratorContractFallback) {
			return true
		}
	}
	return false
}
