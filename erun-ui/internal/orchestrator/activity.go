package orchestrator

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// An agent TUI repaints constantly, so busy state cannot be read off its output;
// the agent reports its own turn boundaries through hooks instead.

// orchestratorActivityTTL bounds a "working" report from a session the desktop
// can no longer see, which may have been killed mid-turn.
const orchestratorActivityTTL = 2 * time.Minute

// orchestratorActivityLiveTTL bounds a visible session's report; a single tool call
// can run long, so reaching it means reporting itself stopped.
const orchestratorActivityLiveTTL = 30 * time.Minute

type orchestratorActivity struct {
	Busy   bool  `json:"busy"`
	AtUnix int64 `json:"atUnix"`
	Serial int   `json:"serial,omitempty"`
}

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
// Unreadable or stale reports answer "not working": a report never invents a spin.
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

func orchestratorActivityDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		configDir = os.TempDir()
	}
	return filepath.Join(configDir, "ERun", "orchestrator-activity")
}

// orchestratorActivityHookCommand writes one report. The settings file is shared,
// so the hook resolves its id from ERUN_ORCHESTRATOR_ID at run time. It uses node
// because PowerShell, the Windows hook shell, misparses POSIX test syntax.
func orchestratorActivityHookCommand(busy bool) string {
	dir := filepath.ToSlash(orchestratorActivityDir())
	state := strconv.FormatBool(busy)
	script := `try{const id=process.env.ERUN_ORCHESTRATOR_ID;if(id){const fs=require("fs");` +
		`fs.mkdirSync("` + dir + `",{recursive:true});` +
		`fs.writeFileSync("` + dir + `/"+id+".json",JSON.stringify({busy:` + state + `,atUnix:Math.floor(Date.now()/1000)}));` +
		`}}catch(e){}`
	return `node -e '` + script + `'`
}

// orchestratorActivityHookMarker identifies our hooks so rewrites replace them and
// leave others' hooks alone.
func orchestratorActivityHookMarker() string {
	return filepath.ToSlash(orchestratorActivityDir())
}

// Anything unreadable is somebody else's and is kept.
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
		if strings.Contains(command, marker) && strings.Contains(command, `busy:`) {
			return true
		}
	}
	return false
}

func orchestratorActivityHookBlock(busy bool) []any {
	return []any{map[string]any{
		"hooks": []any{map[string]any{"type": "command", "command": orchestratorActivityHookCommand(busy)}},
	}}
}

// orchestratorActivityHooks also installs the busy report on tool-call events so a
// long turn keeps renewing it.
func orchestratorActivityHooks() (busyHook, idleHook []any) {
	return orchestratorActivityHookBlock(true), orchestratorActivityHookBlock(false)
}

// mergeOrchestratorHookBlocks preserves the operator's hooks on a shared event and
// drops our previous block to stay idempotent.
func mergeOrchestratorHookBlocks(existing any, ours []any, isOurs func(any) bool) []any {
	current, _ := existing.([]any)
	merged := make([]any, 0, len(current)+len(ours))
	for _, block := range current {
		if isOurs(block) {
			continue
		}
		merged = append(merged, block)
	}
	return append(merged, ours...)
}

// ReconcileOrchestratorActivity re-emits every tick, not only on change, so a
// missed event self-heals; session.aiBusy also feeds Info.Busy for snapshots.
func (s *Service) ReconcileOrchestratorActivity() {
	now := time.Now()
	type row struct {
		id      string
		serial  int
		alive   bool
		agentID string
	}
	var rows []row
	s.withSessions(func(l Registry) {
		rows = make([]row, 0, len(s.orchestrators))
		for id, session := range s.orchestrators {
			if session == nil || session.transient {
				continue
			}
			// A session we can no longer see ages out on the short bound, since nothing else will clear it.
			_, alive := l.Live(id)
			rows = append(rows, row{
				id:      id,
				serial:  session.serial,
				alive:   alive,
				agentID: session.agentID,
			})
		}
	})
	conversations := s.orchestratorAgentConversations(len(rows) > 0)

	for _, r := range rows {
		activity, ok := readOrchestratorActivity(r.id, now, r.alive)
		busy := ok && activity.Busy
		// The shell report is independent of turn busy state, and must name the session
		// that wrote it: sessionAlive is per orchestrator id, so a replaced session's
		// report would otherwise borrow its successor's liveness.
		liveConversationID := conversations[r.agentID]
		shell, shellOK := readOrchestratorShellActivity(r.id, now, r.alive, liveConversationID)
		shellRunning := shellOK && shell.Running
		s.withState(func() {
			if session := s.orchestrators[r.id]; session != nil {
				session.aiBusy = busy
				session.aiBusyAtUnix = activity.AtUnix
				session.shellRunning = shellRunning
				session.shellCommand = shell.Command
				session.shellStartedAtUnix = shell.AtUnix
			}
		})
		s.d.EmitAIActivity(r.serial, busy)
		s.emitOrchestratorShellActivity(r.serial, shellRunning, shell.Command, shell.AtUnix)
	}
}

func (s *Service) emitOrchestratorShellActivity(sessionID int, running bool, command string, startedAtUnix int64) {
	s.emitEvent(uimodel.OrchestratorShellEvent, orchestratorShellActivityPayload{
		SessionID:     sessionID,
		Running:       running,
		Command:       command,
		StartedAtUnix: startedAtUnix,
	})
}

// An unreadable listing returns nil and the replaced-session check is skipped.
func (s *Service) orchestratorAgentConversations(needed bool) map[string]string {
	out := map[string]string{}
	if !needed {
		return out
	}
	agents, err := s.d.ClaudeAgents().List()
	if err != nil {
		return out
	}
	for _, agent := range agents {
		out[agent.ID] = agent.SessionID
	}
	return out
}
