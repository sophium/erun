package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

// CreateOrchestrator persists a new, stopped orchestrator and wires each env's review directory.
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
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, "stopped", 0, orchestratorBusySnapshot{}, false, orchestratorShellSnapshot{}, orchestratorPacingSnapshot{}, a.runtime.ActivitySnapshot(), a.runtime.UsageSnapshot(), false, false)
	return orchestratorInfoWithLaunchSettings(info, launch, false), nil
}

// UpdateOrchestrator replaces the whole entry, so every field comes from the caller:
// the dialog must send back what it was shown, including terminal-set values.
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
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, status, sessionID, busy, false, shell, pacing, a.runtime.ActivitySnapshot(), a.runtime.UsageSnapshot(), restartRequired, roleChanged)
	return orchestratorInfoWithLaunchSettings(info, launch, a.orchestratorLaunchSettingsChanged(configs[index], catalog)), nil
}

// updatedOrchestratorRunningSnapshot reads live session state; zero when nothing runs.
func (a *App) updatedOrchestratorRunningSnapshot(id string, configuredEnvs []configpkg.OrchestratorEnvConfig) (status string, sessionID int, busy orchestratorBusySnapshot, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, restartRequired, roleChanged bool) {
	info, ok := a.runningOrchestratorInfo(id)
	if !ok {
		// Not running is not "never nudged": read the persisted history.
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
	// The running session keeps its spawned scope until Restart, so report that.
	if wired, wiredScope, ok := a.orchestratorWiredEnvs(id); ok {
		restartRequired = orchestratorScopeMismatch(wiredScope, configuredEnvs)
		roleChanged = orchestratorRolesChanged(wired, configuredEnvs)
	}
	return "running", info.SessionID, busy, shell, pacing, restartRequired, roleChanged
}

// StartOrchestrator attaches to the orchestrator's background agent, creating it if needed.
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

// refreshLinkedEnvDirectories re-derives local-agent worktree paths, which can move;
// mirror paths are the operator's choice and stay as persisted.
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

// RestartOrchestrator restarts the background agent on its current conversation.
// Definition and launch resolve before teardown, so bad settings leave the session
// running; transients have no definition and are not restartable.
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
	// The runtime readings are a snapshot taken outside the terminal lock; the card only renders them.
	envActivity, envUsage := a.runtime.ActivitySnapshot(), a.runtime.UsageSnapshot()
	var info orchestratorInfo
	running := false
	a.terminal.WithLock(func(l *terminal.Locked) {
		session := a.orchestrators[id]
		managed := l.Session(terminal.OrchestratorSessionKey(id))
		if session == nil || managed == nil || managed.Closed {
			return
		}
		shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
		pacing := orchestratorPacingSnapshotFromSession(session)
		info = orchestratorInfoFor(session.id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, session.transient, shell, pacing, envActivity, envUsage, false, false)
		running = true
	})
	return info, running
}

// orchestratorWiredEnvs returns what the live session started with, which may differ
// from the current config.
func (a *App) orchestratorWiredEnvs(id string) ([]configpkg.OrchestratorEnvConfig, []string, bool) {
	var envs []configpkg.OrchestratorEnvConfig
	var scope []string
	live := false
	a.terminal.WithLock(func(l *terminal.Locked) {
		session := a.orchestrators[id]
		managed := l.Session(terminal.OrchestratorSessionKey(id))
		if session == nil || managed == nil || managed.Closed {
			return
		}
		envs, scope, live = session.envs, session.wiredScope, true
	})
	return envs, scope, live
}

// Review directories are left out: they can move without changing what is driven.
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

// unreachableOrchestratorIDs reads each orchestrator's own hook report; no in-memory
// state can answer that.
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

// ListOrchestrators merges persisted definitions with transient sessions, id-ordered.
func (a *App) ListOrchestrators() []orchestratorInfo {
	configs, catalog, err := a.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		configs, catalog = nil, nil
	}
	// File reads happen before taking the terminal lock.
	historyEntries, historyUnreadable := readOrchestratorNudgeHistoryEntries(a.deps.orchestratorNudgeHistoryPath)
	unreachable := unreachableOrchestratorIDs(configs, time.Now())
	envActivity, envUsage := a.runtime.ActivitySnapshot(), a.runtime.UsageSnapshot()
	var out []orchestratorInfo
	a.terminal.WithLock(func(l *terminal.Locked) {
		out = make([]orchestratorInfo, 0, len(configs)+len(a.orchestrators))
		seen := make(map[string]struct{}, len(configs))
		for _, config := range configs {
			status := "stopped"
			sessionID := 0
			busy := orchestratorBusySnapshot{}
			shell := orchestratorShellSnapshot{}
			historyEntry, _ := orchestratorNudgeHistoryEntryIn(historyEntries, config.ID)
			pacing := orchestratorPacingSnapshotFromHistory(historyEntry, historyUnreadable)
			// Only a stopped row can be unpaced-but-running.
			pacing.Unreachable = unreachable[config.ID]
			restartRequired := false
			roleChanged := false
			launchSettings := orchestratorLaunchSettingsOf(config)
			launchSettingsChanged := false
			if session := a.orchestrators[config.ID]; session != nil {
				if managed := l.Session(terminal.OrchestratorSessionKey(config.ID)); managed != nil && !managed.Closed {
					status = "running"
					sessionID = session.serial
					busy = orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}
					shell = orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
					pacing = orchestratorPacingSnapshotFromSession(session)
					// The session still runs on its spawned scope.
					restartRequired = orchestratorScopeMismatch(session.wiredScope, config.Environments)
					roleChanged = orchestratorRolesChanged(session.envs, config.Environments)
					launchSettingsChanged = orchestratorLaunchDiffers(config, catalog, session.launched)
				}
			}
			info := orchestratorInfoFor(config.ID, config.Name, config.Alias, config.Environments, config.Directories, status, sessionID, busy, false, shell, pacing, envActivity, envUsage, restartRequired, roleChanged)
			out = append(out, orchestratorInfoWithLaunchSettings(info, launchSettings, launchSettingsChanged))
			seen[config.ID] = struct{}{}
		}
		for id, session := range a.orchestrators {
			if _, ok := seen[id]; ok {
				continue
			}
			managed := l.Session(terminal.OrchestratorSessionKey(id))
			if managed == nil || managed.Closed {
				continue
			}
			shell := orchestratorShellSnapshot{Running: session.shellRunning, Command: session.shellCommand, StartedAtUnix: session.shellStartedAtUnix}
			pacing := orchestratorPacingSnapshotFromSession(session)
			out = append(out, orchestratorInfoFor(id, session.name, session.alias, session.envs, session.dirs, "running", session.serial, orchestratorBusySnapshot{Busy: session.aiBusy, AtUnix: session.aiBusyAtUnix}, true, shell, pacing, envActivity, envUsage, false, false))
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// StopOrchestrator keeps a persisted definition and agent conversation; a transient
// session is forgotten.
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
	var session *orchestratorSession
	var managed *terminal.Session
	a.terminal.WithLock(func(l *terminal.Locked) {
		session = a.orchestrators[id]
		key := terminal.OrchestratorSessionKey(id)
		managed = l.Session(key)
		if session != nil {
			delete(a.orchestrators, id)
		}
		if managed != nil {
			l.Delete(key)
		}
	})
	if managed != nil {
		_ = a.terminal.CloseManaged(managed)
	}
	// A stopped orchestrator stays closed on later launches.
	if err := clearOpenOrchestrator(a.deps.orchestratorOpenPath, id); err != nil {
		log.Printf("erun-app: clear open orchestrator %s: %v", id, err)
	}
	return session != nil || managed != nil
}

// DeleteOrchestrator also clears the nudge history: ids are name-derived and get
// reused by a new orchestrator of the same name.
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
