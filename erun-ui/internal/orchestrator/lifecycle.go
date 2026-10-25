package orchestrator

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
)

// CreateOrchestrator persists a new, stopped orchestrator and wires each env's review directory.
func (s *Service) CreateOrchestrator(name string, envs []EnvInput, dirs []string, alias string, launch LaunchInput) (Info, error) {
	refs, dirRefs, err := s.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return Info{}, err
	}
	alias, launch, _, err = s.validateOrchestratorSettings(alias, launch)
	if err != nil {
		return Info{}, err
	}
	if err := s.linkOrchestratorEnvironments(refs); err != nil {
		return Info{}, err
	}
	configs, err := s.loadOrchestratorConfigs()
	if err != nil {
		return Info{}, err
	}
	id := uniqueOrchestratorID(orchestratorDisplayName(name, refs), configs)
	displayName := orchestratorDisplayName(name, refs)
	def := withOrchestratorLaunchSettings(configpkg.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := s.saveOrchestratorConfigs(append(configs, def)); err != nil {
		return Info{}, err
	}
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, "stopped", 0, orchestratorBusySnapshot{}, false, orchestratorShellSnapshot{}, orchestratorPacingSnapshot{}, s.d.ActivitySnapshot(), s.d.UsageSnapshot(), false, false)
	return orchestratorInfoWithLaunchSettings(info, launch, false), nil
}

// UpdateOrchestrator replaces the whole entry, so every field comes from the caller:
// the dialog must send back what it was shown, including terminal-set values.
func (s *Service) UpdateOrchestrator(id, name string, envs []EnvInput, dirs []string, alias string, launch LaunchInput) (Info, error) {
	id = strings.TrimSpace(id)
	refs, dirRefs, err := s.resolveOrchestratorScope(envs, dirs)
	if err != nil {
		return Info{}, err
	}
	alias, launch, catalog, err := s.validateOrchestratorSettings(alias, launch)
	if err != nil {
		return Info{}, err
	}
	configs, err := s.loadOrchestratorConfigs()
	if err != nil {
		return Info{}, err
	}
	index := -1
	for i, config := range configs {
		if config.ID == id {
			index = i
			break
		}
	}
	if index < 0 {
		return Info{}, fmt.Errorf("orchestrator %q not found", id)
	}
	if err := s.linkOrchestratorEnvironments(refs); err != nil {
		return Info{}, err
	}
	displayName := orchestratorDisplayName(name, refs)
	configs[index] = withOrchestratorLaunchSettings(configpkg.OrchestratorConfig{ID: id, Name: displayName, Alias: alias, Environments: refs, Directories: dirRefs}, launch)
	if err := s.saveOrchestratorConfigs(configs); err != nil {
		return Info{}, err
	}
	status, sessionID, busy, shell, pacing, restartRequired, roleChanged := s.updatedOrchestratorRunningSnapshot(id, refs)
	info := orchestratorInfoFor(id, displayName, alias, refs, dirRefs, status, sessionID, busy, false, shell, pacing, s.d.ActivitySnapshot(), s.d.UsageSnapshot(), restartRequired, roleChanged)
	return orchestratorInfoWithLaunchSettings(info, launch, s.orchestratorLaunchSettingsChanged(configs[index], catalog)), nil
}

// updatedOrchestratorRunningSnapshot reads live session state; zero when nothing runs.
func (s *Service) updatedOrchestratorRunningSnapshot(id string, configuredEnvs []configpkg.OrchestratorEnvConfig) (status string, sessionID int, busy orchestratorBusySnapshot, shell orchestratorShellSnapshot, pacing orchestratorPacingSnapshot, restartRequired, roleChanged bool) {
	info, ok := s.runningOrchestratorInfo(id)
	if !ok {
		// Not running is not "never nudged": read the persisted history.
		entry, _, unreadable := orchestratorNudgeHistoryFor(s.d.NudgeHistoryPath(), id)
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
	if wired, wiredScope, ok := s.orchestratorWiredEnvs(id); ok {
		restartRequired = orchestratorScopeMismatch(wiredScope, configuredEnvs)
		roleChanged = orchestratorRolesChanged(wired, configuredEnvs)
	}
	return "running", info.SessionID, busy, shell, pacing, restartRequired, roleChanged
}

// StartOrchestrator attaches to the orchestrator's background agent, creating it if needed.
func (s *Service) StartOrchestrator(id string, cols, rows int) (Info, error) {
	id = strings.TrimSpace(id)
	if info, ok := s.runningOrchestratorInfo(id); ok {
		return s.withConfiguredLaunchSettings(info), nil
	}
	def, err := s.findOrchestratorConfig(id)
	if err != nil {
		return Info{}, err
	}
	launch, err := s.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return Info{}, err
	}
	return s.spawnOrchestratorSession(orchestratorSpawn{
		id:     def.ID,
		name:   def.Name,
		alias:  def.Alias,
		launch: launch,
		envs:   s.refreshLinkedEnvDirectories(def.Environments),
		dirs:   def.Directories,
		cols:   cols,
		rows:   rows,
	})
}

// refreshLinkedEnvDirectories re-derives local-agent worktree paths, which can move;
// mirror paths are the operator's choice and stay as persisted.
func (s *Service) refreshLinkedEnvDirectories(envs []configpkg.OrchestratorEnvConfig) []configpkg.OrchestratorEnvConfig {
	out := make([]configpkg.OrchestratorEnvConfig, 0, len(envs))
	for _, ref := range envs {
		env, _, err := s.d.Store.LoadEnvConfig(ref.Tenant, ref.Environment)
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
func (s *Service) RestartOrchestrator(id string, cols, rows int) (Info, error) {
	id = strings.TrimSpace(id)
	def, err := s.findOrchestratorConfig(id)
	if err != nil {
		return Info{}, err
	}
	launch, err := s.resolvePersistedOrchestratorLaunch(def)
	if err != nil {
		return Info{}, err
	}
	s.StopOrchestratorSession(id)
	return s.spawnOrchestratorSession(orchestratorSpawn{
		id:      def.ID,
		name:    def.Name,
		alias:   def.Alias,
		launch:  launch,
		envs:    s.refreshLinkedEnvDirectories(def.Environments),
		dirs:    def.Directories,
		restart: true,
		cols:    cols,
		rows:    rows,
	})
}

func (s *Service) runningOrchestratorInfo(id string) (Info, bool) {
	// The runtime readings are a snapshot taken outside the terminal lock; the card only renders them.
	envActivity, envUsage := s.d.ActivitySnapshot(), s.d.UsageSnapshot()
	var info Info
	running := false
	s.withSessions(func(l Registry) {
		session := s.orchestrators[id]
		if _, open := l.Live(id); session == nil || !open {
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
func (s *Service) orchestratorWiredEnvs(id string) ([]configpkg.OrchestratorEnvConfig, []string, bool) {
	var envs []configpkg.OrchestratorEnvConfig
	var scope []string
	live := false
	s.withSessions(func(l Registry) {
		session := s.orchestrators[id]
		if _, open := l.Live(id); session == nil || !open {
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
func (s *Service) ListOrchestrators() []Info {
	configs, catalog, err := s.loadOrchestratorConfigsWithCatalog()
	if err != nil {
		configs, catalog = nil, nil
	}
	// File reads happen before taking the terminal lock.
	historyEntries, historyUnreadable := readOrchestratorNudgeHistoryEntries(s.d.NudgeHistoryPath())
	unreachable := unreachableOrchestratorIDs(configs, time.Now())
	envActivity, envUsage := s.d.ActivitySnapshot(), s.d.UsageSnapshot()
	var out []Info
	s.withSessions(func(l Registry) {
		out = make([]Info, 0, len(configs)+len(s.orchestrators))
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
			if session := s.orchestrators[config.ID]; session != nil {
				if _, open := l.Live(config.ID); open {
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
		for id, session := range s.orchestrators {
			if _, ok := seen[id]; ok {
				continue
			}
			if _, open := l.Live(id); !open {
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
func (s *Service) StopOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	stopped := s.StopOrchestratorSession(id)
	_, err := s.findOrchestratorConfig(id)
	if err != nil {
		if stopped {
			return nil
		}
		return fmt.Errorf("orchestrator %q not found", id)
	}
	if err := s.stopOrchestratorAgent(id); err != nil {
		return fmt.Errorf("stop the Claude background agent for %s: %w", id, err)
	}
	return nil
}

func (s *Service) StopOrchestratorSession(id string) bool {
	var session *orchestratorSession
	var managed Terminal
	var registered bool
	s.withSessions(func(l Registry) {
		session = s.orchestrators[id]
		if session != nil {
			delete(s.orchestrators, id)
		}
		managed, registered = l.Remove(id)
	})
	if registered {
		managed.Close()
	}
	// A stopped orchestrator stays closed on later launches.
	if err := clearOpenOrchestrator(s.d.OpenPath(), id); err != nil {
		log.Printf("erun-app: clear open orchestrator %s: %v", id, err)
	}
	return session != nil || registered
}

// DeleteOrchestrator also clears the nudge history: ids are name-derived and get
// reused by a new orchestrator of the same name.
func (s *Service) DeleteOrchestrator(id string) error {
	id = strings.TrimSpace(id)
	s.StopOrchestratorSession(id)
	if err := s.deleteOrchestratorAgent(id); err != nil {
		return fmt.Errorf("remove the Claude background agent for %s: %w", id, err)
	}
	if err := clearOrchestratorNudgeHistoryEntry(s.d.NudgeHistoryPath(), id); err != nil {
		log.Printf("erun-app: clear orchestrator nudge history %s: %v", id, err)
	}
	configs, err := s.loadOrchestratorConfigs()
	if err != nil {
		return err
	}
	filtered := make([]configpkg.OrchestratorConfig, 0, len(configs))
	for _, config := range configs {
		if config.ID != id {
			filtered = append(filtered, config)
		}
	}
	return s.saveOrchestratorConfigs(filtered)
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

// TransientSpawn is a one-off session seeded with InitialPrompt. It has no
// definition, so it is never paced, recorded as open or reattached.
type TransientSpawn struct {
	ID            string
	Name          string
	Envs          []configpkg.OrchestratorEnvConfig
	InitialPrompt string
	Cols          int
	Rows          int
}

func (s *Service) SpawnTransient(spawn TransientSpawn) (Info, error) {
	return s.spawnOrchestratorSession(orchestratorSpawn{
		id:            spawn.ID,
		name:          spawn.Name,
		launch:        defaultOrchestratorLaunchPlan(),
		envs:          spawn.Envs,
		initialPrompt: spawn.InitialPrompt,
		transient:     true,
		cols:          spawn.Cols,
		rows:          spawn.Rows,
	})
}

// Running reports whether id has a live session on this desktop.
func (s *Service) Running(id string) bool {
	_, running := s.runningOrchestratorInfo(id)
	return running
}

// PersistedLaunchError is why starting id's definition now would be refused,
// resolved exactly as that start resolves it; nil when it would start or id is unknown.
func (s *Service) PersistedLaunchError(id string) error {
	def, found := s.persistedDefinition(id)
	if !found {
		return nil
	}
	_, err := s.resolvePersistedOrchestratorLaunch(def)
	return err
}

func (s *Service) persistedDefinition(id string) (configpkg.OrchestratorConfig, bool) {
	def, err := s.findOrchestratorConfig(id)
	return def, err == nil
}
