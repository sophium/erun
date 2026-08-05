package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// newOrchestratorStubStore backs the store with a persistent in-memory root
// config plus a remote-agent env (frs/dev) to link and a local-agent env
// (frs/laptop) that must be filtered out of the candidates.
func newOrchestratorStubStore() stubUIStore {
	return stubUIStore{
		config: &eruncommon.ERunConfig{},
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev":    {Name: "dev", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx"},
			"frs/laptop": {Name: "laptop", Type: eruncommon.EnvironmentTypeLocalAgent},
		},
	}
}

// orchestratorTestApp confines $HOME to a temp dir so ensureOrchestratorWorkspace
// and the default mirror directories never touch the real home.
func orchestratorTestApp(t *testing.T) *App {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	return NewApp(erunUIDeps{
		store: newOrchestratorStubStore(),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
	})
}

func TestListOrchestratorEnvCandidatesReturnsOnlyRemoteAgents(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	candidates, err := app.ListOrchestratorEnvCandidates()
	if err != nil {
		t.Fatalf("ListOrchestratorEnvCandidates failed: %v", err)
	}
	if len(candidates) != 1 || candidates[0].Tenant != "frs" || candidates[0].Environment != "dev" {
		t.Fatalf("expected only the remote-agent env, got %+v", candidates)
	}
	if !strings.HasSuffix(candidates[0].DefaultDirectory, "orchestrators"+string(os.PathSeparator)+"frs-dev") {
		t.Fatalf("unexpected default directory: %q", candidates[0].DefaultDirectory)
	}
}

// assertSingleOrchestratorStatus fails unless the app lists exactly one
// orchestrator in wantStatus.
func assertSingleOrchestratorStatus(t *testing.T, app *App, wantStatus string) {
	t.Helper()
	got := app.ListOrchestrators()
	if len(got) != 1 || got[0].Status != wantStatus {
		t.Fatalf("expected one %s orchestrator, got %+v", wantStatus, got)
	}
}

// assertLoneTenant fails unless tenants is exactly [want].
func assertLoneTenant(t *testing.T, tenants []string, want string) {
	t.Helper()
	if len(tenants) != 1 || tenants[0] != want {
		t.Fatalf("expected the lone tenant %q, got %+v", want, tenants)
	}
}

// assertSingleOrchestratorID fails unless the app lists exactly one
// orchestrator with wantID.
func assertSingleOrchestratorID(t *testing.T, app *App, wantID string) {
	t.Helper()
	listed := app.ListOrchestrators()
	if len(listed) != 1 || listed[0].ID != wantID {
		t.Fatalf("expected the orchestrator to persist, got %+v", listed)
	}
}

// assertSingleTransientOrchestrator fails unless the app lists exactly one
// transient orchestrator.
func assertSingleTransientOrchestrator(t *testing.T, app *App) {
	t.Helper()
	listed := app.ListOrchestrators()
	if len(listed) != 1 || !listed[0].Transient {
		t.Fatalf("expected one transient investigator listed, got %+v", listed)
	}
}

// assertCreatedFRSOrchestrator checks the shape of a freshly created
// single-env "frs" orchestrator.
func assertCreatedFRSOrchestrator(t *testing.T, info orchestratorInfo) {
	t.Helper()
	if info.Status != "stopped" || len(info.Environments) != 1 || info.Environments[0].Directory == "" {
		t.Fatalf("unexpected created orchestrator: %+v", info)
	}
	assertLoneTenant(t, info.Tenants, "frs")
}

func TestCreateOrchestratorWiresSyncAndPersists(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.CreateOrchestrator("no envs", nil); err == nil {
		t.Fatal("expected an orchestrator with no linked environments to be rejected")
	}

	info, err := app.CreateOrchestrator(
		"validation agent",
		[]orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}},
	)
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	assertCreatedFRSOrchestrator(t, info)

	// The env's one-way workspace sync was wired to the mirror directory.
	env, _, err := app.deps.store.LoadEnvConfig("frs", "dev")
	if err != nil {
		t.Fatalf("load env: %v", err)
	}
	if !env.SSHD.Enabled || !env.SSHD.WorkspaceSync.Enabled ||
		env.SSHD.WorkspaceSync.LocalPath != info.Environments[0].Directory {
		t.Fatalf("expected workspace sync wired to the mirror dir, got %+v", env.SSHD)
	}
	// The mirror directory was created.
	if fi, statErr := os.Stat(info.Environments[0].Directory); statErr != nil || !fi.IsDir() {
		t.Fatalf("expected the mirror directory to be created: %v", statErr)
	}
	// Persisted.
	assertSingleOrchestratorID(t, app, info.ID)
}

func TestRestartOrchestratorRespawnsWithFreshSession(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if started.Status != "running" || started.SessionID == 0 {
		t.Fatalf("expected a running session, got %+v", started)
	}

	restarted, err := app.RestartOrchestrator(created.ID, 80, 24)
	if err != nil {
		t.Fatalf("RestartOrchestrator failed: %v", err)
	}
	if restarted.Status != "running" || restarted.ID != created.ID {
		t.Fatalf("expected the same orchestrator running after restart, got %+v", restarted)
	}
	// A restart tears the old session down and spawns a new one, so the serial must
	// advance — reusing the old serial would attach the UI to a dead PTY.
	if restarted.SessionID == started.SessionID {
		t.Fatalf("expected a fresh session serial after restart, both were %d", started.SessionID)
	}

	// Transient / unknown ids have no persisted definition, so they are not restartable.
	if _, err := app.RestartOrchestrator("does-not-exist", 80, 24); err == nil {
		t.Fatal("expected restart of an unknown orchestrator to error")
	}
}

func TestSpawnOrchestratorSessionExposesOrchestratorID(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	var capturedEnv []string
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(),
		startTerminal: func(p startTerminalSessionParams) (terminalSession, error) {
			capturedEnv = p.Env
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) { return "claude-stub", nil, nil },
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	// The agent reads its own id from $ERUN_ORCHESTRATOR_ID to record the return
	// target for a rebuild+restart.
	want := "ERUN_ORCHESTRATOR_ID=" + created.ID
	found := false
	for _, entry := range capturedEnv {
		if entry == want {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected the session env to carry %q, got %+v", want, capturedEnv)
	}
}

func TestUpdateOrchestratorRelinksEnvironments(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator(
		"agent",
		[]orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}},
	)
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	updated, err := app.UpdateOrchestrator(
		created.ID,
		"agent renamed",
		[]orchestratorEnvInput{
			{Tenant: "frs", Environment: "dev", Directory: t.TempDir()},
		},
	)
	if err != nil {
		t.Fatalf("UpdateOrchestrator failed: %v", err)
	}
	if updated.Name != "agent renamed" {
		t.Fatalf("expected the edit to apply, got %+v", updated)
	}

	// Start keeps the definition; delete removes it.
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	assertSingleOrchestratorStatus(t, app, "running")
	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}
	assertSingleOrchestratorStatus(t, app, "stopped")
	if err := app.DeleteOrchestrator(created.ID); err != nil {
		t.Fatalf("DeleteOrchestrator failed: %v", err)
	}
	if got := app.ListOrchestrators(); len(got) != 0 {
		t.Fatalf("expected no orchestrators after delete, got %+v", got)
	}
}

func TestInvestigateFailureSpawnsTransientTenantScopedOrchestrator(t *testing.T) {
	var seededPrompt string
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(_, prompt, _, _ string) (string, []string, error) {
			seededPrompt = prompt
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())

	info, err := app.InvestigateFailure("deploy failed: helm timeout", "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	if !info.Transient {
		t.Fatalf("expected a transient investigator, got %+v", info)
	}
	assertLoneTenant(t, info.Tenants, "frs")
	if !strings.Contains(seededPrompt, "erun-investigate") || !strings.Contains(seededPrompt, "erun-file-issue") {
		t.Fatalf("unexpected seed prompt: %q", seededPrompt)
	}
	start := strings.Index(seededPrompt, "saved at ") + len("saved at ")
	end := strings.Index(seededPrompt, ". Read it")
	if start < len("saved at ") || end <= start {
		t.Fatalf("could not locate the report path in the prompt: %q", seededPrompt)
	}
	data, readErr := os.ReadFile(seededPrompt[start:end])
	if readErr != nil {
		t.Fatalf("staged report file: %v", readErr)
	}
	if !strings.Contains(string(data), "helm timeout") {
		t.Fatalf("staged report missing the failure detail: %q", data)
	}
	assertSingleTransientOrchestrator(t, app)
}

func TestOrchestratorWorkspaceIsSharedRootWithOneClaudeMd(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace failed: %v", err)
	}
	if dir != orchestratorsRoot() {
		t.Fatalf("expected the shared orchestrators root %q, got %q", orchestratorsRoot(), dir)
	}
	// Calling again is idempotent and resolves to the same single workspace — there
	// is no per-orchestrator folder.
	if again, _ := app.ensureOrchestratorWorkspace(); again != dir {
		t.Fatalf("expected one shared workspace, got %q then %q", dir, again)
	}

	data, readErr := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if readErr != nil {
		t.Fatalf("read CLAUDE.md: %v", readErr)
	}
	for _, want := range []string{"erun-orchestrate", "Never edit", "uninterrupted", "end-to-end", "`<tenant>-<env>`", "already operating under this contract", "ERUN_ORCHESTRATOR_ID"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("shared orchestrator CLAUDE.md missing %q:\n%s", want, data)
		}
	}
	// The shared CLAUDE.md is generic — no per-orchestrator "Linked environments" list.
	if strings.Contains(string(data), "## Linked environments") {
		t.Fatalf("expected no per-orchestrator env list in the shared CLAUDE.md:\n%s", data)
	}
}

func TestOrchestratorSkillsInstalledByDefault(t *testing.T) {
	// Fixture skills source standing in for the repo's erun-skills/skills.
	srcRoot := t.TempDir()
	for _, name := range []string{"erun-orchestrate", "erun-file-issue"} {
		dir := filepath.Join(srcRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ERUN_SKILLS_DIR", srcRoot)

	app := orchestratorTestApp(t) // confines $HOME to a temp dir
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	skillsRoot := filepath.Join(home, ".claude", "skills")
	for _, name := range []string{"erun-orchestrate", "erun-file-issue"} {
		if _, err := os.Stat(filepath.Join(skillsRoot, name, "SKILL.md")); err != nil {
			t.Fatalf("skill %q not installed by default: %v", name, err)
		}
	}

	// Idempotent + edit-preserving: a hand-edited installed skill survives a re-run.
	edited := filepath.Join(skillsRoot, "erun-orchestrate", "SKILL.md")
	if err := os.WriteFile(edited, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace failed: %v", err)
	}
	if data, _ := os.ReadFile(edited); string(data) != "EDITED" {
		t.Fatalf("expected in-place skill edit preserved, got %q", data)
	}
}

// installedOrchestrateSkill is the path an installed erun-orchestrate SKILL.md
// lands at under the test's confined $HOME.
func installedOrchestrateSkill(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills", "erun-orchestrate", "SKILL.md")
}

// singleSkillSource writes a one-skill fixture source (erun-orchestrate) and
// points ERUN_SKILLS_DIR at it, returning the source SKILL.md path.
func singleSkillSource(t *testing.T, body string) string {
	t.Helper()
	srcRoot := t.TempDir()
	skillDir := filepath.Join(srcRoot, "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ERUN_SKILLS_DIR", srcRoot)
	return srcMD
}

func TestOrchestratorSkillsRefreshWhenSourceChanges(t *testing.T) {
	srcMD := singleSkillSource(t, "# v1\n")
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
	if data, _ := os.ReadFile(installed); string(data) != "# v1\n" {
		t.Fatalf("expected installed v1, got %q", data)
	}

	// A newer skill ships; an untouched install must track it on next launch.
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# v2 newer\n" {
		t.Fatalf("expected untouched install refreshed to v2, got %q", data)
	}
}

func TestOrchestratorSkillsPreserveEditAcrossSourceChange(t *testing.T) {
	srcMD := singleSkillSource(t, "# v1\n")
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)

	// Operator edits the installed skill; then a newer source ships. The edit
	// must win over the refresh.
	if err := os.WriteFile(installed, []byte("# operator edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# operator edit\n" {
		t.Fatalf("expected operator edit preserved across a source change, got %q", data)
	}
}

func TestOrchestratorSessionStartHookInjectsContract(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".claude", "settings.json"))
	if err != nil {
		t.Fatalf("read orchestrator settings.json: %v", err)
	}
	var settings struct {
		Hooks struct {
			SessionStart []struct {
				Matcher string `json:"matcher"`
				Hooks   []struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"hooks"`
			} `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	matchers := map[string]bool{}
	for _, group := range settings.Hooks.SessionStart {
		matchers[group.Matcher] = true
		if len(group.Hooks) == 0 || group.Hooks[0].Type != "command" {
			t.Fatalf("SessionStart matcher %q missing a command hook:\n%s", group.Matcher, data)
		}
		if !strings.Contains(group.Hooks[0].Command, "CLAUDE.md") {
			t.Fatalf("SessionStart command does not inject the contract (print CLAUDE.md):\n%s", group.Hooks[0].Command)
		}
	}
	for _, want := range []string{"startup", "resume"} {
		if !matchers[want] {
			t.Fatalf("SessionStart hook missing matcher %q:\n%s", want, data)
		}
	}
}

func TestOrchestratorSessionStartHookPreservesExistingSettings(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	claudeDir := filepath.Join(orchestratorsRoot(), ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A pre-existing project settings file with an unrelated key and hook event.
	pre := `{"model":"opus","hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"echo keep"}]}]}}`
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), []byte(pre), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}

	data, _ := os.ReadFile(filepath.Join(claudeDir, "settings.json"))
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	if settings["model"] != "opus" {
		t.Fatalf("expected unrelated key preserved, got %v\n%s", settings["model"], data)
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if _, ok := hooks["PreToolUse"]; !ok {
		t.Fatalf("expected unrelated hook event preserved:\n%s", data)
	}
	if _, ok := hooks["SessionStart"]; !ok {
		t.Fatalf("expected SessionStart hook added:\n%s", data)
	}
}

func TestBuildOrchestratorLaunchResumesWithUltracodeOpus(t *testing.T) {
	// No pinned session id (legacy/transient) keeps the continue-else-fresh path.
	_, posix := buildOrchestratorLaunch("linux", "", false, "", "", "")
	posixCmd := posix[len(posix)-1]
	for _, want := range []string{"claude --continue", "ultracode", "--model opus", " || claude"} {
		if !strings.Contains(posixCmd, want) {
			t.Fatalf("posix launch missing %q: %q", want, posixCmd)
		}
	}

	_, win := buildOrchestratorLaunch("windows", "", false, "", "", "")
	winCmd := win[len(win)-1]
	for _, want := range []string{"claude --continue", "ultracode", "--model opus", "$LASTEXITCODE"} {
		if !strings.Contains(winCmd, want) {
			t.Fatalf("windows launch missing %q: %q", want, winCmd)
		}
	}

	// A seeded (Investigate) launch is a fresh session — no resume.
	_, seeded := buildOrchestratorLaunch("linux", "", false, "fix the thing", "", "")
	seededCmd := seeded[len(seeded)-1]
	if strings.Contains(seededCmd, "--continue") {
		t.Fatalf("seeded launch must not resume: %q", seededCmd)
	}
	if !strings.Contains(seededCmd, "ultracode") || !strings.Contains(seededCmd, "--model opus") {
		t.Fatalf("seeded launch missing ultracode/opus: %q", seededCmd)
	}
}

// A pinned session id isolates each orchestrator: it resumes its OWN conversation
// (`--resume <id>` when it exists, `--session-id <id>` to create it on first
// open) and never `--continue`s the shared most-recent conversation.
func TestBuildOrchestratorLaunchPinsPerOrchestratorSession(t *testing.T) {
	const sid = "6f7e9c2a-1b3d-4e5f-8a9b-000000000001"

	_, existing := buildOrchestratorLaunch("linux", sid, true, "", "", "")
	cmd := existing[len(existing)-1]
	if !strings.Contains(cmd, "--resume "+sid) || strings.Contains(cmd, "--continue") {
		t.Fatalf("existing session must --resume its own id, not --continue: %q", cmd)
	}

	_, firstOpen := buildOrchestratorLaunch("linux", sid, false, "", "", "")
	fresh := firstOpen[len(firstOpen)-1]
	if !strings.Contains(fresh, "--session-id "+sid) {
		t.Fatalf("first open must create the pinned session: %q", fresh)
	}
	if strings.Contains(fresh, "--continue") || strings.Contains(fresh, "--resume") {
		t.Fatalf("first open must not continue/resume: %q", fresh)
	}

	_, win := buildOrchestratorLaunch("windows", sid, true, "", "finish the task", "")
	if wcmd := win[len(win)-1]; !strings.Contains(wcmd, "--resume "+sid) || !strings.Contains(wcmd, "finish the task") {
		t.Fatalf("windows pinned resume+prompt missing pieces: %q", wcmd)
	}
}

// Per-orchestrator session ids are deterministic and unique so each orchestrator
// resumes its own conversation, while a transient (empty-id) orchestrator has no
// pinned session.
func TestOrchestratorSessionIDIsStableAndDistinct(t *testing.T) {
	if orchestratorSessionID("va1") == orchestratorSessionID("petios1") {
		t.Fatal("distinct orchestrators must derive distinct session ids")
	}
	if id := orchestratorSessionID("va1"); id != orchestratorSessionID("va1") {
		t.Fatal("session id must be stable for the same orchestrator")
	}
	if orchestratorSessionID("") != "" {
		t.Fatal("transient orchestrator (empty id) must have no pinned session")
	}
}

// A resume prompt resumes the conversation AND hands it the prompt, on both
// shells, with a fresh-session fallback that carries the same prompt so an
// auto-resume runs the task even on a first launch with no prior conversation.
func TestBuildOrchestratorLaunchResumeWithPromptRunsIt(t *testing.T) {
	const prompt = "verify the rebuild is live, then finish the task"

	_, posix := buildOrchestratorLaunch("linux", "", false, "", prompt, "")
	posixCmd := posix[len(posix)-1]
	for _, want := range []string{"claude --continue", prompt, " || claude", "ultracode", "--model opus"} {
		if !strings.Contains(posixCmd, want) {
			t.Fatalf("posix resume+prompt launch missing %q: %q", want, posixCmd)
		}
	}
	// The prompt must appear on both the resume and the fresh-fallback branch.
	if strings.Count(posixCmd, prompt) < 2 {
		t.Fatalf("expected the resume prompt on both branches: %q", posixCmd)
	}

	_, win := buildOrchestratorLaunch("windows", "", false, "", prompt, "")
	winCmd := win[len(win)-1]
	for _, want := range []string{"claude --continue", prompt, "$LASTEXITCODE"} {
		if !strings.Contains(winCmd, want) {
			t.Fatalf("windows resume+prompt launch missing %q: %q", want, winCmd)
		}
	}

	// initialPrompt still wins over resumePrompt (a seeded fresh session ignores resume).
	_, seeded := buildOrchestratorLaunch("linux", "", false, "fix the thing", prompt, "")
	if seededCmd := seeded[len(seeded)-1]; strings.Contains(seededCmd, "--continue") {
		t.Fatalf("initialPrompt must take precedence over resumePrompt (fresh, no resume): %q", seededCmd)
	}
}

func TestInvestigateFailureRejectsEmptyReport(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	if _, err := app.InvestigateFailure("   ", "frs", "dev", 80, 24); err == nil {
		t.Fatal("expected an empty failure report to be rejected")
	}
}
