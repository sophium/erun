package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// CreateOrchestrator persists a new orchestrator: it prepares each linked env's
// host review directory (creating the mirror and wiring its sync where the env
// needs one), then stores the definition. Created stopped — StartOrchestrator
// spawns the session.
func (a *App) CreateOrchestrator(name string, envs []orchestratorEnvInput, dirs []string, alias string, launch orchestratorLaunchInput) (orchestratorInfo, error) {
	refs, dirRefs, err := a.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	alias, launch, _, err = a.validateOrchestratorSettings(alias, launch)
	if err != nil {
		return orchestratorInfo{}, err
	}
	if err := a.linkOrchestratorEnvironments(refs); err != nil {
		return orchestratorInfo{}, err
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return orchestratorInfo{}, err
	}
	id := uniqueOrchestratorID(orchestratorDisplayName(name, refs), configs)
	displayName := orchestratorDisplayName(name, refs)
	def := withOrchestratorLaunchSettings(configpkg.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := a.saveOrchestratorConfigs(append(configs, def)); err != nil {
		return orchestratorInfo{}, err
	}
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, "stopped", 0, orchestratorBusySnapshot{}, false, orchestratorShellSnapshot{}, orchestratorPacingSnapshot{}, a.envActivitySnapshot(), a.envUsageSnapshot(), false, false)
	return orchestratorInfoWithLaunchSettings(info, launch, false), nil
}

// UpdateOrchestrator edits an existing orchestrator's linked environments, the
// directories it names for itself, its name, the platform alias it acts as, and
// its gateway, model and effort, re-wiring sync for the current set.
//
// Every field comes from the caller rather than being read back off the stored
// definition, because the save below replaces the whole entry: a field this
// literal omits is not left alone, it is erased. alias and the launch settings
// in particular are settable from the terminal (`erun orchestrator set-*`) as
// well as from this dialog, so the dialog must send back what it was shown even
// when the operator edited something else entirely.
func (a *App) UpdateOrchestrator(id, name string, envs []orchestratorEnvInput, dirs []string, alias string, launch orchestratorLaunchInput) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	refs, dirRefs, err := a.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return orchestratorInfo{}, err
	}
	alias, launch, catalog, err := a.validateOrchestratorSettings(alias, launch)
	if err != nil {
		return orchestratorInfo{}, err
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return orchestratorInfo{}, err
	}
	index := -1
	for i, config := range configs {
		if config.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return orchestratorInfo{}, fmt.Errorf("orchestrator %q not found", id)
	}
	if err := a.linkOrchestratorEnvironments(refs); err != nil {
		return orchestratorInfo{}, err
	}
	displayName := orchestratorDisplayName(name, refs)
	configs[index] = withOrchestratorLaunchSettings(configpkg.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := a.saveOrchestratorConfigs(configs); err != nil {
		return orchestratorInfo{}, err
	}
	status, sessionID, busy, shell, pacing, restartRequired, roleChanged := a.updatedOrchestratorRunningSnapshot(id, refs)
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, status, sessionID, busy, false, shell, pacing, a.envActivitySnapshot(), a.envUsageSnapshot(), restartRequired, roleChanged)
	return orchestratorInfoWithLaunchSettings(info, launch, a.orchestratorLaunchSettingsChanged(configs[index], catalog)), nil
}

// updatedOrchestratorRunningSnapshot is UpdateOrchestrator's own read of live
// session state, split out to keep that function's complexity within budget.
// "stopped" (the zero snapshots, restartRequired false) when nothing is
// running for id.
func (a *App) updatedOrchestratorRunningSnapshot(id string, configuredEnvs []configpkg.OrchestratorEnvConfig) (status string, sessionID int, busy orchestratorBusySnapshot, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, restartRequired, roleChanged bool) {
	info, ok := a.runningOrchestratorInfo(id)
	if !ok {
		// Not running is not "never nudged": read the persisted cumulative
		// history the same way ListOrchestrators' stopped branch does, so
		// saving an edit to a stopped orchestrator does not flash its hover
		// card back to "Not nudged".
		entry, _, unreadable := orchestratorNudgeHistoryFor(a.deps.orchestratorNudgeHistoryPath, id)
		return "stopped", 0, orchestratorBusySnapshot{}, orchestratorShellSnapshot{}, orchestratorPacingSnapshotFromHistory(entry, unreadable), false, false
	}
	busy = orchestratorBusySnapshot{Busy: info.Busy, AtUnix: info.BusyAtUnix}
	shell = orchestratorShellSnapshot{Running: info.ShellRunning, Command: info.ShellCommand, StartedAtUnix: info.ShellStartedAtUnix}
	pacing = orchestratorPacingSnapshot{
		NudgeCount:          info.NudgeCount,
		Capped:              info.NudgeCapped,
		LastNudgeAtUnix:     info.LastNudgeAtUnix,
		AutoNudgeCount:      info.AutoNudgeCount,
		LastAutoNudgeAtUnix: info.LastAutoNudgeAtUnix,
		WhipCount:           info.WhipCount,
		LastWhipAtUnix:      info.LastWhipAtUnix,
		LastCappedAtUnix:    info.LastCappedAtUnix,
		HistoryUnreadable:   info.NudgeHistoryUnreadable,
	}
	// The session already running still holds whatever it was spawned with;
	// saving a different scope here does not touch it (see
	// orchestratorScopeMismatch). Restart is the only thing that re-wires it,
	// so the operator is told rather than shown "running" as if the save took
	// effect immediately. A role-only edit doesn't affect wiring the same way
	// (see orchestratorRolesChanged), but is still reported so the dialog can
	// tell the operator their edit is not guaranteed live yet.
	if wired, wiredScope, ok := a.orchestratorWiredEnvs(id); ok {
		restartRequired = orchestratorScopeMismatch(wiredScope, configuredEnvs)
		roleChanged = orchestratorRolesChanged(wired, configuredEnvs)
	}
	return "running", info.SessionID, busy, shell, pacing, restartRequired, roleChanged
}

// StartOrchestrator opens a persisted orchestrator: it attaches to the
// orchestrator's Claude background agent, creating the agent when there is
// none, and reuses a PTY that is already attached.
func (a *App) StartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	if info, ok := a.runningOrchestratorInfo(id); ok {
		return a.withConfiguredLaunchSettings(info), nil
	}
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	launch, err := a.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return orchestratorInfo{}, err
	}
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:     def.ID,
		name:   def.Name,
		alias:  def.Alias,
		launch: launch,
		envs:   a.refreshLinkedEnvDirectories(def.Environments),
		dirs:   def.Directories,
		cols:   cols,
		rows:   rows,
	})
}

// refreshLinkedEnvDirectories re-derives each local-agent link's review directory
// from the env config as a session starts, so an orchestrator keeps reviewing a
// worktree whose repository path moved after it was linked. A mirror path is the
// operator's own choice, so it is left exactly as persisted.
func (a *App) refreshLinkedEnvDirectories(envs []configpkg.OrchestratorEnvConfig) []configpkg.OrchestratorEnvConfig {
	out := make([]configpkg.OrchestratorEnvConfig, 0, len(envs))
	for _, ref := range envs {
		env, _, err := a.deps.store.LoadEnvConfig(ref.Tenant, ref.Environment)
		if err == nil {
			if derived, mirrored := orchestratorReviewDirectory(ref.Tenant, env); !mirrored && derived != "" {
				ref.Directory = derived
			}
		}
		out = append(out, ref)
	}
	return out
}

// RestartOrchestrator stops a persisted orchestrator's background agent and
// starts it again, so the operator can recycle a stuck agent, or apply changed
// settings, without losing context: the agent comes back on its own current
// conversation and re-reads the settings and MCP config written for it. The
// definition is resolved before teardown because stopOrchestratorSession
// forgets the live session, and a transient (Investigate) orchestrator has no
// persisted definition to respawn — findOrchestratorConfig then returns an
// error, so transients are intentionally not restartable. The launch is
// resolved before teardown too, so settings that cannot launch leave the
// running session in place rather than stopping it for nothing.
func (a *App) RestartOrchestrator(id string, cols, rows int) (orchestratorInfo, error) {
	id = strings.TrimSpace(id)
	def, err := a.findOrchestratorConfig(id)
	if err != nil {
		return orchestratorInfo{}, err
	}
	launch, err := a.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return orchestratorInfo{}, err
	}
	a.stopOrchestratorSession(id)
	return a.spawnOrchestratorSession(orchestratorSpawn{
		id:      def.ID,
		name:    def.Name,
		alias:   def.Alias,
		launch:  launch,
		envs:    a.refreshLinkedEnvDirectories(def.Environments),
		dirs:    def.Directories,
		restart: true,
		cols:    cols,
		rows:    rows,
	})
}

func (a *App) runningOrchestratorInfo(id string) (orchestratorInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return orchestratorInfo{}, false
	}
	shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
	pacing := orchestratorPacingSnapshotFromSession(session)
	return orchestratorInfoFor(session.id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, session.transient, shell, pacing, a.envActivity, a.envUsage, false, false), true
}

// orchestratorWiredEnvs returns the environments id's live session was opened
// with and the scope its agent's MCP config actually holds — what the agent
// last started with, not necessarily what the orchestrator is configured with
// right now — or ok=false when nothing is running for it.
func (a *App) orchestratorWiredEnvs(id string) ([]configpkg.OrchestratorEnvConfig, []string, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	session := a.orchestrators[id]
	managed := a.sessions[orchestratorSessionKey(id)]
	if session == nil || managed == nil || managed.closed {
		return nil, nil, false
	}
	return session.envs, session.wiredScope, true
}

// orchestratorScopeOf renders linked environments as sorted tenant/environment
// pairs. Review directories are deliberately left out: where an environment is
// reviewed on this host can move without changing what the orchestrator drives.
func orchestratorScopeOf(envs []configpkg.OrchestratorEnvConfig) []string {
	out := make([]string, 0, len(envs))
	for _, env := range envs {
		tenant, environment := strings.TrimSpace(env.Tenant), strings.TrimSpace(env.Environment)
		if tenant == "" || environment == "" {
			continue
		}
		out = append(out, tenant+"/"+environment)
	}
	sort.Strings(out)
	return out
}

// unreachableOrchestratorIDs names every configured orchestrator this desktop
// cannot pace from here: one report read each, since the question is whether the
// orchestrator's OWN hooks are reporting a session, which no in-memory state can
// answer. See orchestratorPacingUnreachable.
func unreachableOrchestratorIDs(configs []configpkg.OrchestratorConfig, now time.Time) map[string]bool {
	out := make(map[string]bool, len(configs))
	for _, config := range configs {
		id := strings.TrimSpace(config.ID)
		if id == "" || !orchestratorPacingUnreachable(id, now) {
			continue
		}
		out[id] = true
	}
	return out
}

// ListOrchestrators merges the persisted definitions (each tagged running or
// stopped) with any transient running sessions (Investigate), id-ordered.
func (a *App) ListOrchestrators() []orchestratorInfo {
	// The catalog comes from the same read: each running row resolves its
	// definition against it to tell whether a restart would change anything.
	configs, catalog, err := a.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		configs, catalog = nil, nil
	}
	// Read once for the whole list rather than once per orchestrator: the
	// "stopped" branch below has no live session to read cumulative pacing
	// history from, so it falls back to this persisted record instead of the
	// zero snapshot a stopped orchestrator otherwise reported unconditionally.
	historyEntries, historyUnreadable := readOrchestratorNudgeHistoryEntries(a.deps.orchestratorNudgeHistoryPath)
	// Gathered before the lock for the same reason the history above is: one
	// small report read per configured orchestrator, and the result is only
	// consulted for the ones this desktop turns out to hold no session for.
	unreachable := unreachableOrchestratorIDs(configs, time.Now())
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]orchestratorInfo, 0, len(configs)+len(a.orchestrators))
	seen := make(map[string]struct{}, len(configs))
	for _, config := range configs {
		status := "stopped"
		sessionID := 0
		busy := orchestratorBusySnapshot{}
		shell := orchestratorShellSnapshot{}
		historyEntry, _ := orchestratorNudgeHistoryEntryIn(historyEntries, config.ID)
		pacing := orchestratorPacingSnapshotFromHistory(historyEntry, historyUnreadable)
		// Only the stopped branch can be unpaced-but-running: a running one is
		// a session this desktop holds, and the branch below rebuilds the
		// snapshot from that session, which drops this flag by construction.
		pacing.Unreachable = unreachable[config.ID]
		restartRequired := false
		roleChanged := false
		launchSettings := orchestratorLaunchSettingsOf(config)
		launchSettingsChanged := false
		if session := a.orchestrators[config.ID]; session != nil {
			if managed := a.sessions[orchestratorSessionKey(config.ID)]; managed != nil && !managed.closed {
				status = "running"
				sessionID = session.serial
				busy = orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}
				shell = orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
				pacing = orchestratorPacingSnapshotFromSession(session)
				// The persisted config is the operator's intent; the session
				// still runs on whatever it was spawned with. A poll that
				// shows the new set as if it were live is exactly the defect
				// this flag exists to stop (see orchestratorScopeMismatch).
				restartRequired = orchestratorScopeMismatch(session.wiredScope, config.Environments)
				roleChanged = orchestratorRolesChanged(session.envs, config.Environments)
				launchSettingsChanged = orchestratorLaunchDiffers(config, catalog, session.launched)
			}
		}
		info := orchestratorInfoFor(config.ID, config.Name, config.Alias, config.Environments, config.Directories, status, sessionID, busy, false, shell, pacing, a.envActivity, a.envUsage, restartRequired, roleChanged)
		out = append(out, orchestratorInfoWithLaunchSettings(info, launchSettings, launchSettingsChanged))
		seen[config.ID] = struct{}{}
	}
	for id, session := range a.orchestrators {
		if _, ok := seen[id]; ok {
			continue
		}
		managed := a.sessions[orchestratorSessionKey(id)]
		if managed == nil || managed.closed {
			continue
		}
		shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
		pacing := orchestratorPacingSnapshotFromSession(session)
		out = append(out, orchestratorInfoFor(id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, true, shell, pacing, a.envActivity, a.envUsage, false, false))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StopOrchestrator ends a running session while keeping a persisted definition:
// it detaches the desktop's terminal and stops the orchestrator's background
// agent, whose conversation is kept for the next start. A transient session is
// forgotten entirely.
func (a *App) StopOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	stopped := a.stopOrchestratorSession(id)
	_, err := a.findOrchestratorConfig(id)
	if err != nil {
		if stopped {
			return nil
		}
		return fmt.Errorf("orchestrator %q not found", id)
	}
	if err := a.stopOrchestratorAgent(id); err != nil {
		return fmt.Errorf("stop the Claude background agent for %s: %w", id, err)
	}
	return nil
}

func (a *App) stopOrchestratorSession(id string) bool {
	a.mu.Lock()
	session := a.orchestrators[id]
	key := orchestratorSessionKey(id)
	managed := a.sessions[key]
	if session != nil {
		delete(a.orchestrators, id)
	}
	if managed != nil {
		delete(a.sessions, key)
	}
	a.mu.Unlock()
	if managed != nil {
		_ = a.closeManaged(managed)
	}
	// Stopping is the operator saying this orchestrator should not come back, so
	// it stays closed on every later launch. A restart clears and re-records in
	// the same breath, which is the same statement about what is open.
	if err := clearOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
		log.Printf("erun-app: clear open orchestrator %s: %v", id, err)
	}
	return session != nil || managed != nil
}

// DeleteOrchestrator stops the session if running, removes the orchestrator's
// background agent, and removes the persisted definition. The linked
// environments' sync config is left intact, and so is the agent's conversation
// transcript, which is Claude's.
//
// It also clears this id's persisted nudge history: the id is a
// name-derived slug (uniqueOrchestratorID), not a uuid, so creating a new
// orchestrator with the same name after this delete reuses it. Leaving the
// old record in place would hand that unrelated new orchestrator a nudge
// history that was never its own.
func (a *App) DeleteOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	a.stopOrchestratorSession(id)
	if err := a.deleteOrchestratorAgent(id); err != nil {
		return fmt.Errorf("remove the Claude background agent for %s: %w", id, err)
	}
	if err := clearOrchestratorNudgeHistoryEntry(a.deps.orchestratorNudgeHistoryPath, id); err != nil {
		log.Printf("erun-app: clear orchestrator nudge history %s: %v", id, err)
	}
	configs, err := a.loadOrchestratorConfigs()
	if err != nil {
		return err
	}
	filtered := make([]configpkg.OrchestratorConfig, 0, len(configs))
	for _, config := range configs {
		if config.ID != id {
			filtered = append(filtered, config)
		}
	}
	return a.saveOrchestratorConfigs(filtered)
}

// uniqueOrchestratorID derives a stable, human-readable id from the name that
// does not collide with an existing definition.
func uniqueOrchestratorID(name string, existing []configpkg.OrchestratorConfig) string {
	stem := orchestratorIDStem(name)
	taken := make(map[string]struct{}, len(existing))
	for _, config := range existing {
		taken[config.ID] = struct{}{}
	}
	candidate := stem
	for i := 2; ; i++ {
		if _, dup := taken[candidate]; !dup {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d", stem, i)
	}
}

// orchestratorIDStem slugifies name into a stable id stem, falling back to
// "orchestrator" when the name carries no id-safe characters.
func orchestratorIDStem(name string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			slug.WriteRune(r)
		case r == ' ' || r == '-' || r == '_' || r == ',':
			slug.WriteByte('-')
		}
	}
	stem := strings.Trim(slug.String(), "-")
	if stem == "" {
		return "orchestrator"
	}
	return stem
}
