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

// orchestratorActivityTTL bounds how long a "working" report stays believable.
// A session killed mid-turn never writes its end, and without a bound its row
// would spin for as long as the desktop ran — the same failure by a different
// route.
const orchestratorActivityTTL = 2 * time.Minute

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
// An unreadable, unparseable or stale file answers "not working": the report can
// only keep a row spinning, never invent a spin, which is the same direction the
// pod heartbeat is allowed to push an env's row.
func readOrchestratorActivity(id string, now time.Time) (orchestratorActivity, bool) {
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
	if activity.AtUnix <= 0 || now.Sub(time.Unix(activity.AtUnix, 0)) > orchestratorActivityTTL {
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

// orchestratorActivityHooks are the turn boundaries. UserPromptSubmit opens a
// turn; Stop closes it. SessionStart closes one too, so a session resumed after
// being killed mid-turn does not inherit the previous run's "working".
func orchestratorActivityHooks() (busyHook, idleHook []any) {
	wrap := func(command string) []any {
		return []any{map[string]any{
			"hooks": []any{map[string]any{"type": "command", "command": command}},
		}}
	}
	return wrap(orchestratorActivityHookCommand(true)), wrap(orchestratorActivityHookCommand(false))
}
