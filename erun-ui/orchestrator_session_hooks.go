package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// orchestratorContractFallback is printed when the shared CLAUDE.md is somehow
// missing, so a session still boots knowing it is under the contract.
const orchestratorContractFallback = "You are a host-side erun orchestrator. Read and follow the CLAUDE.md in this directory and the erun-orchestrate skill before doing anything, even a trivial-looking question."

// orchestratorSkillHookCommand is the SessionStart hook command written into the
// orchestrators root's .claude/settings.json. It INJECTS the operating contract
// into the session by printing the shared CLAUDE.md to plain stdout (added to the
// session context), so an orchestrator always has its contract in context instead
// of being asked to load a skill it can skip. Plain stdout — not JSON — sidesteps
// the 10,000-char additionalContext cap.
//
// Runs through node rather than cat/echo chained with a POSIX shell: this is a
// SessionStart hook, and Windows' own hook shell (PowerShell) parses
// `[ -n ... ]` test syntax as something else entirely rather than executing
// it. Node reads each file directly and writes its bytes straight to stdout,
// so neither file's body ever passes through the shell at all -- only the
// two forward-slashed paths are baked into the script, and node resolves
// identically regardless of the host's own hook shell.
func orchestratorSkillHookCommand(dir string) string {
	claudeMd := filepath.ToSlash(filepath.Join(dir, "CLAUDE.md"))
	// The per-orchestrator layer, injected AFTER the shared contract so the
	// ordering is the precedence rule: a role file can add to the common
	// contract or override a line of it, and a reader can see which won.
	//
	// ERUN_ORCHESTRATOR_ID is set at launch and resolved at run time, since
	// this settings file is shared by every orchestrator. A transient
	// (Investigate) session has an empty id by design, so the guard skips it
	// and it gets the shared contract only.
	//
	// Absent file is a silent no-op: most orchestrators will not have one, and
	// a missing file must neither fail the hook nor write to stderr.
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
// rewritten. Deliberately the inverse of the shared CLAUDE.md, which erun
// overwrites on every launch: one layer is erun's and always current, the other
// is the operator's and always preserved. That asymmetry is the feature.
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

// ensureOrchestratorRoleFile creates the per-orchestrator role file if it is
// absent and leaves it strictly alone otherwise. Called on every launch rather
// than only at creation, so an orchestrator that already existed gets one too.
// An empty id (a transient session) seeds nothing.
//
// The id is slugified to [a-z0-9-] by orchestratorIDStem, so for any id erun
// mints the filename is safe by construction; it is still resolved with an exact
// filepath.Join and never a glob, so a hand-edited config.yaml id cannot widen
// what gets read.
func ensureOrchestratorRoleFile(dir, id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil
	}
	path := filepath.Join(dir, "CLAUDE."+id+".md")
	if _, err := os.Stat(path); err == nil {
		return nil // the operator's file; never rewritten
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(path, []byte(orchestratorRoleFileSeed), 0o644)
}

// ensureOrchestratorSessionStartHook writes a SessionStart hook into the shared
// orchestrators root's .claude/settings.json, merging so it never clobbers other
// keys, hook events, or hook blocks already there -- including a SessionStart
// block the operator added themselves. SessionStart fires on a new session
// (startup), a reopened one (resume), a cleared one (clear), and a compacted
// one (compact), so every orchestrator has its contract re-injected on every
// session boundary rather than only the first two.
func ensureOrchestratorSessionStartHook(dir string) error {
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(claudeDir, "settings.json")
	settings := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &settings) // best-effort; the hook is rebuilt below regardless
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	hooks["SessionStart"] = mergeOrchestratorHookBlocks(
		hooks["SessionStart"], orchestratorSessionStartHook(dir), isOrchestratorSessionStartHookBlock)
	// The agent reports its own turn boundaries. Whether it is working cannot be
	// read off its terminal: an agent TUI repaints continuously, so an
	// output-driven latch never clears.
	//
	// The busy report goes on the tool-call events as well as the turn's start, so
	// a turn longer than the staleness bound renews it instead of letting it
	// expire underneath work that is still running. Each event is merged rather
	// than assigned: this settings file is shared with the operator, and the
	// tool-call events in particular are somewhere they are likely to have hooks
	// of their own, which an assignment would silently delete.
	busyHook, idleHook := orchestratorActivityHooks()
	for _, event := range []string{"UserPromptSubmit", "PreToolUse", "PostToolUse"} {
		hooks[event] = mergeOrchestratorHookBlocks(hooks[event], busyHook, isOrchestratorActivityHookBlock)
	}
	stop := mergeOrchestratorHookBlocks(hooks["Stop"], idleHook, isOrchestratorActivityHookBlock)
	hooks["Stop"] = mergeOrchestratorHookBlocks(stop, orchestratorNoAskStopGuardBlock(), isOrchestratorNoAskStopGuardBlock)
	// A background shell's start and its completion are both only visible in a
	// tool call's own result, which PreToolUse never carries — so unlike the
	// turn-boundary busy report above, this one lives on PostToolUse alone.
	// See orchestrator_shell_activity.go.
	hooks["PostToolUse"] = mergeOrchestratorHookBlocks(
		hooks["PostToolUse"], orchestratorShellActivityHookBlocks(), isOrchestratorShellActivityHookBlock)
	// Not installing the retired conversation recorders is not enough: every
	// machine that ever ran an older build still carries them, and they would go
	// on writing files nothing reads on every turn boundary. Strip them wherever
	// they are found.
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

// orchestratorNoAskGuardMarker identifies the stop guard this file wrote, so a
// rewrite replaces its own previous block instead of stacking another copy.
const orchestratorNoAskGuardMarker = "noask_guard=1"

// orchestratorNoAskStopGuardReason is fed back to the session when the guard
// fires. ASCII-only and apostrophe-free, since it is embedded as a plain JS
// string inside a script wrapped in a single-quoted shell argument -- a
// literal single quote in the text would close that argument early.
const orchestratorNoAskStopGuardReason = "Your closing message hands the operator a decision. " +
	"The orchestrator contract resolves ambiguity itself and carries the task to a verified end, " +
	"so a question is a defect, not caution. Do what you offered, or state it as a decision already taken, " +
	"and finish. If the action is outward-facing, announce it and proceed - a heads-up is not a gate."

// orchestratorNoAskStopGuardCommand refuses a turn that ends by handing the
// operator a decision, which the launch flag cannot reach: denying the tool
// removes the question form, not the closing sentence that stalls the work just
// as long. Reading the turn's own last words is what puts the guarantee at the
// layer the behaviour surfaces on.
//
// Every failure path falls through to node's default exit code, 0. A guard
// that wedged a session on a transcript it could not parse would cost more
// than the stalls it prevents, and an already nudged turn (stop_hook_active)
// is let go so a session is corrected once rather than looped.
//
// Runs through node rather than a POSIX shell reading its own stdin with sed
// and grep: this fires on every Stop event of every orchestrator, and
// Windows' own hook shell (PowerShell) parses `[ -f ... ]` test syntax as
// something else entirely rather than executing it. Node needs no helper
// binary on PATH -- the AI harness that launched the session is itself an
// npm package -- and resolves identically regardless of the host's own hook
// shell.
//
// It reads the turn's last ASSISTANT entry, never the raw tail of the
// transcript. A firing is itself recorded in the transcript, and the record
// carries this command — every trigger phrase included — so a window of raw
// lines matches the guard's own echo from then on and the session can never
// end again. It is also what let an operator's own question ("would you like
// me to…") refuse the reply that answered it. Only what the turn said can
// decide whether the turn handed back a decision.
//
// Within that entry, only the turn's own unquoted words count. A report that
// quotes an earlier violation -- or the contract, or the operator -- is
// describing an offer, not making one, and re-firing on it refuses the very
// turn that documents the fix. Quoted, code-spanned, and blockquoted spans are
// cut before the trigger phrases are matched, so a genuine offer still fires
// and a citation of one does not.
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

// orchestratorNoAskStopGuardBlock is the guard bound to the Stop event.
func orchestratorNoAskStopGuardBlock() []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorNoAskStopGuardCommand()}},
	}}
}

// isOrchestratorNoAskStopGuardBlock reports whether a settings hook block is the
// guard. Anything it cannot read is somebody else's and is kept.
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

// orchestratorSessionStartMatcher is a single pattern matching every source
// this hook cares about, rather than separate matcher blocks carrying the same
// three commands: SessionStart's matcher is tested as a regex against the
// event's source, and alternation is already how this file scopes a matcher to
// more than one value (see orchestratorShellActivityHookBlocks' "TaskOutput|
// TaskStop"). Several blocks with identical hooks would mean every SessionStart
// command is stored multiple times on disk for no behavioral gain.
//
// clear and compact both fire SessionStart, and both were missing here (#1232):
// a /clear drops the injected CLAUDE.<id>.md role layer from context even
// though the file survives on disk, and a compaction forks the transcript to a
// new session id that the live-session record then never picks up. Printing
// the contract again on either is harmless (plain stdout, not a file append);
// omitting it is not.
const orchestratorSessionStartMatcher = "startup|resume|clear|compact"

// orchestratorSessionStartHook is the SessionStart hook block: the contract-
// injecting command runs on new starts (startup), reopens (resume), and every
// session boundary that can otherwise drop it from context (clear, compact).
func orchestratorSessionStartHook(dir string) []any {
	command := map[string]any{"type": "command", "command": orchestratorSkillHookCommand(dir)}
	// A session killed mid-turn never writes its end, so a new or reopened one
	// would inherit the previous run's "working" and spin on arrival with nothing
	// running. Clearing it here is what makes that guarantee real.
	idle := map[string]any{"type": "command", "command": orchestratorActivityHookCommand(false)}
	// A background shell report has the exact same hazard: a shell the previous
	// run started (or its clear) can outlive that run entirely, and nothing else
	// resets it at a boundary — see orchestrator_shell_activity.go.
	shellIdle := map[string]any{"type": "command", "command": orchestratorShellActivityResetHookCommand()}
	return []any{map[string]any{
		"matcher": orchestratorSessionStartMatcher,
		"hooks":   []any{command, idle, shellIdle},
	}}
}

// isOrchestratorSessionStartHookBlock reports whether a settings hook block is
// one of ours, so a rewrite recognizes and replaces any block(s) it previously
// wrote -- whether that is today's single combined-matcher block or the pair of
// per-source blocks an earlier version of this file installed -- instead of
// stacking another copy beside them. The contract-injection command's fallback
// text is unique to us and present regardless of which shape wrote it.
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
