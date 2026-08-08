package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Whether an orchestrator is working cannot be read off its terminal. The
// sidebar's latch was driven by output reads, released after three seconds of
// silence — and an agent TUI is never silent for three seconds, because it
// repaints its prompt, spinner and counter continuously. So the latch set when
// work began and then stayed set forever, with the desktop burning CPU reading
// an idle session's redraws to keep it that way.
//
// The agent knows when its turn starts and ends, so it says so. erun already
// composes the orchestrator's launch and already writes hooks into the shared
// workspace, so the report costs a file write per turn and replaces a guess
// about pixels with a statement about work.

// orchestratorActivityTTL bounds how long a "working" report stays believable
// once the desktop can no longer see the session that wrote it. A session
// killed mid-turn never writes its end, and without a bound its row would spin
// for as long as the desktop ran — the same failure by a different route.
const orchestratorActivityTTL = 2 * time.Minute

// orchestratorActivityLiveTTL is that bound for a session the desktop can still
// see. A turn is not a short thing: it runs as long as the work does, and a
// single tool call inside it — a build, a bounded wait on a detached job — can
// outlast any interval worth calling "recent". Timing a live session out on the
// short bound answered "not working" for the most ordinary case there is, an
// orchestrator that is simply still working.
//
// A live session is bounded too, only far enough out that reaching it means the
// reporting itself has stopped rather than the turn being long. Liveness is what
// separates the two cases the short bound had to resolve identically; this keeps
// the short bound for the case it was actually written for.
const orchestratorActivityLiveTTL = 30 * time.Minute

type orchestratorActivity struct {
	Busy   bool  `json:"busy"`
	AtUnix int64 `json:"atUnix"`
	Serial int   `json:"serial,omitempty"`
}

// orchestratorActivityPath is the file one orchestrator's hooks write and the
// desktop reads. Per-orchestrator, beside the other per-orchestrator state.
func orchestratorActivityPath(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return ""
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-activity", id+".json")
}

// readOrchestratorActivity reports whether this orchestrator says it is working.
// sessionAlive is whether the desktop can still see the session that writes the
// report, and it selects which staleness bound applies: a report from a session
// still in front of us ages out on the live bound, one from a session we can no
// longer see on the short one.
//
// An unreadable, unparseable or stale file answers "not working": the report can
// only keep a row spinning, never invent a spin, which is the same direction the
// pod heartbeat is allowed to push an env's row.
func readOrchestratorActivity(id string, now time.Time, sessionAlive bool) (orchestratorActivity, bool) {
	path := orchestratorActivityPath(id)
	if path == "" {
		return orchestratorActivity{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return orchestratorActivity{}, false
	}
	var activity orchestratorActivity
	if err := json.Unmarshal(data, &activity); err != nil {
		return orchestratorActivity{}, false
	}
	ttl := orchestratorActivityTTL
	if sessionAlive {
		ttl = orchestratorActivityLiveTTL
	}
	if activity.AtUnix <= 0 || now.Sub(time.Unix(activity.AtUnix, 0)) > ttl {
		return orchestratorActivity{}, false
	}
	return activity, true
}

// orchestratorActivityDir is the directory the reports live in. The hooks
// resolve their own file inside it at run time.
func orchestratorActivityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-activity")
}

// orchestratorActivityHookCommand writes one report.
//
// The orchestrators workspace is shared, so its settings file is too — one hook
// serves every orchestrator. It therefore resolves which one it is at run time
// from ERUN_ORCHESTRATOR_ID, the id the session already carries; baking an id in
// would have every orchestrator reporting as whichever one wrote the file last.
// A session without that variable (transient/Investigate) writes nothing rather
// than a file named for nobody.
//
// It is a bare shell redirect rather than a helper binary on purpose: this runs
// at every turn boundary of every orchestrator, and a hook that needed erun on
// PATH would stop reporting exactly when the environment is misconfigured.
func orchestratorActivityHookCommand(busy bool) string {
	dir := filepath.ToSlash(orchestratorActivityDir())
	state := strconv.FormatBool(busy)
	return `[ -n "$ERUN_ORCHESTRATOR_ID" ] && mkdir -p "` + dir + `" && ` +
		`printf '{"busy":` + state + `,"atUnix":%s}' "$(date +%s)" > "` + dir + `/$ERUN_ORCHESTRATOR_ID.json"` +
		` || true`
}

// orchestratorActivityHookMarker identifies a hook this file wrote, so a rewrite
// replaces its own previous block instead of stacking another copy beside it,
// and so merging into an event leaves everyone else's hooks alone.
func orchestratorActivityHookMarker() string {
	return filepath.ToSlash(orchestratorActivityDir())
}

// isOrchestratorActivityHookBlock reports whether a settings hook block is one
// of ours. Anything it cannot read is somebody else's and is kept.
func isOrchestratorActivityHookBlock(block any) bool {
	group, ok := block.(map[string]any)
	if !ok {
		return false
	}
	hooks, ok := group["hooks"].([]any)
	if !ok {
		return false
	}
	marker := orchestratorActivityHookMarker()
	for _, hook := range hooks {
		entry, ok := hook.(map[string]any)
		if !ok {
			continue
		}
		command, ok := entry["command"].(string)
		if !ok {
			continue
		}
		if strings.Contains(command, marker) && strings.Contains(command, `"busy":`) {
			return true
		}
	}
	return false
}

// orchestratorActivityHookBlock is one report bound to whichever event it is
// installed on.
func orchestratorActivityHookBlock(busy bool) []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorActivityHookCommand(busy)}},
	}}
}

// orchestratorActivityHooks are the reports the settings file installs.
//
// The busy report is not bound to the turn's start alone. A turn boundary is the
// only place work is *known* to begin and end, but it is not the only place the
// report has to exist: a report written once at the start of a turn is stale for
// every minute of that turn after the bound, and the row it should have kept
// spinning goes quiet while the work is still running. Installing the same busy
// report on the tool-call events renews it from the thing a working orchestrator
// does constantly, at the cost of the same bare redirect.
func orchestratorActivityHooks() (busyHook, idleHook []any) {
	return orchestratorActivityHookBlock(true), orchestratorActivityHookBlock(false)
}

// mergeOrchestratorActivityHook installs ours on an event without disturbing
// what is already bound to it. The workspace settings file is shared with the
// operator, so an event we write to may already carry their hooks; replacing the
// event outright would delete them. Our own previous block is dropped first so
// this stays idempotent across restarts.
func mergeOrchestratorActivityHook(existing any, ours []any) []any {
	current, _ := existing.([]any)
	merged := make([]any, 0, len(current)+len(ours))
	for _, block := range current {
		if isOrchestratorActivityHookBlock(block) {
			continue
		}
		merged = append(merged, block)
	}
	return append(merged, ours...)
}
