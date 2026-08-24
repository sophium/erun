package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	eruncommon "github.com/sophium/erun/erun-common"
)

// newOrchestratorStubStore backs the store with a persistent in-memory root
// config plus one env of each agent type — remote-agent frs/dev, reviewed in a
// mirror, and local-agent frs/laptop, reviewed in the worktree at laptopRepoPath
// — plus a runtime env, which has no worktree to review and no agent to delegate
// to and so is not orchestratable.
func newOrchestratorStubStore(laptopRepoPath string) stubUIStore {
	return stubUIStore{
		config: &eruncommon.ERunConfig{},
		tenants: map[string]eruncommon.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]eruncommon.EnvConfig{
			"frs/dev":     {Name: "dev", Type: eruncommon.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx"},
			"frs/laptop":  {Name: "laptop", Type: eruncommon.EnvironmentTypeLocalAgent, LocalRepoPath: laptopRepoPath},
			"frs/runtime": {Name: "runtime", Type: eruncommon.EnvironmentTypeRuntime},
		},
	}
}

// orchestratorTestApp confines $HOME to a temp dir so ensureOrchestratorWorkspace
// and the default mirror directories never touch the real home.
func orchestratorTestApp(t *testing.T) *App {
	t.Helper()
	app, _ := orchestratorTestAppWithLocalRepo(t)
	return app
}

// investigateHelmTimeoutReport is the shape the desktop's failure card produces:
// the command that failed, the target, the error, and the captured output. The
// bounds in investigation_bounds.go admit it because it carries all three.
const investigateHelmTimeoutReport = `erun deploy failed
Target: frs/dev
Version: 1.0.179
Release: frs-devops
Namespace: frs-dev
Started: 2026-08-16T06:01:12Z
Elapsed: 4s

Error: ==> Deploy failed after 4s

Output:
helm upgrade --install frs-devops ./chart
Error: UPGRADE FAILED: timed out waiting for the condition`

// orchestratorTestAppWithLocalRepo also returns the local-agent env's worktree
// path, for the assertions that care where that env is reviewed.
func orchestratorTestAppWithLocalRepo(t *testing.T) (*App, string) {
	t.Helper()
	return orchestratorTestAppWithReachability(t, orchestratorTestAlwaysReachable)
}

// orchestratorTestAppWithReachability is orchestratorTestAppWithLocalRepo with
// an injectable MCP-edge reachability probe, so a test about the unreachable-
// edge notice can simulate a down edge without a real port-forward,
// while every other orchestrator test gets a deterministic "always reachable"
// default instead of depending on a real network dial.
func orchestratorTestAppWithReachability(t *testing.T, reachable func(int) bool) (*App, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	// Resolve the shipped skills to an empty directory by default, so a test
	// about something else never trips over the report a desktop posts when no
	// skills source resolves at all. Tests that are about the skills stage their
	// own source AFTER calling this.
	t.Setenv("ERUN_SKILLS_DIR", t.TempDir())
	laptopRepo := t.TempDir()
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(laptopRepo),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint: reachable,
	})
	// Stage failure reports inside the test's own directory. Left at the default
	// they land in the shared host temp dir, which is how a suite that spawns
	// nothing still left two reports per run behind — and made a handful of real
	// failures read as dozens of spawned agents.
	app.investigations.reportDir = t.TempDir()
	return app, laptopRepo
}

// TestListOrchestratorEnvCandidatesCoversBothAgentTypes locks the capability: an
// orchestrator can link either agent type, and each candidate carries where that
// env is reviewed — a mirror the operator may place anywhere, or the local-agent
// worktree already on this machine. A runtime env stays out: nothing to review.
func TestListOrchestratorEnvCandidatesCoversBothAgentTypes(t *testing.T) {
	app, laptopRepo := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	candidates, err := app.ListOrchestratorEnvCandidates()
	if err != nil {
		t.Fatalf("ListOrchestratorEnvCandidates failed: %v", err)
	}
	if len(candidates) != 2 {
		t.Fatalf("expected both agent envs and no runtime env, got %+v", candidates)
	}
	dev, laptop := candidates[0], candidates[1]
	if dev.Environment != "dev" || laptop.Environment != "laptop" {
		t.Fatalf("expected dev then laptop, got %+v", candidates)
	}
	if !dev.Mirrored {
		t.Fatalf("expected the remote-agent env to be reviewed in a mirror, got %+v", dev)
	}
	if !strings.HasSuffix(dev.DefaultDirectory, "orchestrators"+string(os.PathSeparator)+"frs-dev") {
		t.Fatalf("unexpected mirror directory: %q", dev.DefaultDirectory)
	}
	if laptop.Mirrored {
		t.Fatalf("expected the local-agent env to be reviewed in place, got %+v", laptop)
	}
	if laptop.DefaultDirectory != laptopRepo {
		t.Fatalf("local-agent review directory = %q, want the env worktree %q", laptop.DefaultDirectory, laptopRepo)
	}
}

// TestCreateOrchestratorLinksLocalAgentWithoutSync locks the other half: a
// local-agent env's worktree is already on this machine, so linking it must not
// switch SSHD or workspace sync on behind the operator's back, and the review
// directory is derived from the env rather than taken from the caller.
func TestCreateOrchestratorLinksLocalAgentWithoutSync(t *testing.T) {
	app, laptopRepo := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	info, err := app.CreateOrchestrator("laptop agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "laptop", Directory: filepath.Join(t.TempDir(), "ignored")},
	})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Environments) != 1 || info.Environments[0].Directory != laptopRepo {
		t.Fatalf("expected the env worktree %q as the review directory, got %+v", laptopRepo, info.Environments)
	}
	env, _, err := app.deps.store.LoadEnvConfig("frs", "laptop")
	if err != nil {
		t.Fatalf("load env: %v", err)
	}
	if env.SSHD.Enabled || env.SSHD.WorkspaceSync.Enabled {
		t.Fatalf("expected sync left off for a local-agent env, got %+v", env.SSHD)
	}
}

// TestCreateOrchestratorRejectsLocalAgentWithoutWorktree keeps the failure loud:
// with no worktree to read, the orchestrator would otherwise be handed an empty
// review window that looks like an env with no changes.
func TestCreateOrchestratorRejectsLocalAgentWithoutWorktree(t *testing.T) {
	app, _ := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	missing := filepath.Join(t.TempDir(), "not-there")
	store := app.deps.store.(stubUIStore)
	store.envs["frs/gone"] = eruncommon.EnvConfig{Name: "gone", Type: eruncommon.EnvironmentTypeLocalAgent, LocalRepoPath: missing}
	store.envs["frs/unset"] = eruncommon.EnvConfig{Name: "unset", Type: eruncommon.EnvironmentTypeLocalAgent}

	if _, err := app.CreateOrchestrator("gone", []orchestratorEnvInput{{Tenant: "frs", Environment: "gone"}}); err == nil {
		t.Fatal("expected a local-agent env whose worktree is absent to be rejected")
	}
	if _, err := app.CreateOrchestrator("unset", []orchestratorEnvInput{{Tenant: "frs", Environment: "unset"}}); err == nil {
		t.Fatal("expected a local-agent env with no repository path to be rejected")
	}
}

// TestRefreshLinkedEnvDirectoriesFollowsAMovedWorktree covers the derived path
// staying correct: a local-agent link follows its env's repository path, while a
// mirror path the operator chose is left alone.
func TestRefreshLinkedEnvDirectoriesFollowsAMovedWorktree(t *testing.T) {
	app, _ := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	moved := t.TempDir()
	store := app.deps.store.(stubUIStore)
	store.envs["frs/laptop"] = eruncommon.EnvConfig{Name: "laptop", Type: eruncommon.EnvironmentTypeLocalAgent, LocalRepoPath: moved}
	chosenMirror := filepath.Join(t.TempDir(), "my-mirror")

	refreshed := app.refreshLinkedEnvDirectories([]eruncommon.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "laptop", Directory: "/stale/path"},
		{Tenant: "frs", Environment: "dev", Directory: chosenMirror},
	})
	if refreshed[0].Directory != moved {
		t.Fatalf("local-agent directory = %q, want the moved worktree %q", refreshed[0].Directory, moved)
	}
	if refreshed[1].Directory != chosenMirror {
		t.Fatalf("mirror directory = %q, want the operator's choice %q", refreshed[1].Directory, chosenMirror)
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
		store: newOrchestratorStubStore(t.TempDir()),
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
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(_, prompt, _, _ string) (string, []string, error) {
			seededPrompt = prompt
			return "claude-stub", nil, nil
		},
	})
	app.investigations.reportDir = t.TempDir()
	defer app.shutdown(context.Background())

	info, err := app.InvestigateFailure(investigateHelmTimeoutReport, "frs", "dev", 80, 24)
	if err != nil {
		t.Fatalf("InvestigateFailure failed: %v", err)
	}
	if !info.Transient {
		t.Fatalf("expected a transient investigator, got %+v", info)
	}
	assertLoneTenant(t, info.Tenants, "frs")
	if !strings.Contains(seededPrompt, "report-") || !strings.Contains(seededPrompt, "erun-file-issue") {
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
	if !strings.Contains(string(data), "UPGRADE FAILED") {
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
	for _, want := range []string{"erun-orchestrate", "Never write into a review directory", "local-agent", "uninterrupted", "end-to-end", "`<tenant>-<env>`", "already operating under this contract", "ERUN_ORCHESTRATOR_ID"} {
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
	app := orchestratorTestApp(t) // confines $HOME to a temp dir
	defer app.shutdown(context.Background())

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
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleSkillSource(t, "# v1\n")

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
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleSkillSource(t, "# v1\n")

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

// clearSkillsOverride removes the exact-source override so a test exercises the
// resolution a desktop actually runs with.
func clearSkillsOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_SKILLS_DIR", "")
}

// runFromBareLayout puts the running binary where the desktop actually runs
// from: a directory with no checkout above it, so nothing but the build stamp
// can name the skills this build ships. Pinned rather than inherited because a
// test binary's own location is not the test's to choose — one compiled into the
// checkout would otherwise resolve the repo's own skills and quietly stop
// exercising the layout the test is about.
func runFromBareLayout(t *testing.T) string {
	t.Helper()
	exeDir := filepath.Join(t.TempDir(), "dev-bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(exeDir, "erun-app")
	previous := runningExecutable
	runningExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { runningExecutable = previous })
	return exeDir
}

// stampedSkillsCheckout stands in for the checkout a desktop build was produced
// from, in the layout that motivated the stamp: the running binary sits nowhere
// near a source tree, so the walk up from the executable resolves nothing and
// only the build stamp names the skills this build shipped. Returns the source
// SKILL.md.
func stampedSkillsCheckout(t *testing.T, body string) string {
	t.Helper()
	clearSkillsOverride(t)
	runFromBareLayout(t)
	skillsRoot := filepath.Join(t.TempDir(), "checkout", "erun-skills", "skills")
	skillDir := filepath.Join(skillsRoot, "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stampSkillsSource(t, skillsRoot)
	return srcMD
}

// stampSkillsSource sets the build stamp for one test and restores it after.
func stampSkillsSource(t *testing.T, dir string) {
	t.Helper()
	previous := buildSkillsSource
	buildSkillsSource = dir
	t.Cleanup(func() { buildSkillsSource = previous })
}

// orchestratorTestAppWithEmits captures the frontend events the app posts, so a
// test can assert what the operator is actually told.
func orchestratorTestAppWithEmits(t *testing.T) (*App, *capturedEmits) {
	t.Helper()
	app := orchestratorTestApp(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	return app, emits
}

// TestOrchestratorSkillsRefreshFromBuildStampedCheckout covers the layout the
// desktop is actually built and run from: the bundle is copied out of its
// checkout, so nothing above the running binary names a source tree and only the
// build stamp resolves one. An installed copy erun itself wrote must track that
// source instead of freezing at whatever landed on first install.
func TestOrchestratorSkillsRefreshFromBuildStampedCheckout(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())
	srcMD := stampedSkillsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
	if data, _ := os.ReadFile(installed); string(data) != "# v1\n" {
		t.Fatalf("expected the stamped checkout's skill installed, got %q", data)
	}

	// The source changes and the desktop is restarted: the untouched install
	// must follow it.
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# v2 newer\n" {
		t.Fatalf("expected the untouched install refreshed from the stamped checkout, got %q", data)
	}
	// The marker moves with the refresh, so the next launch can still tell this
	// untouched copy from an edited one.
	marker, err := os.ReadFile(filepath.Join(filepath.Dir(installed), orchestratorSkillMarker))
	if err != nil {
		t.Fatalf("read skill marker: %v", err)
	}
	want, err := fileSHA256(srcMD)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(marker)) != want {
		t.Fatalf("marker = %q, want the refreshed source sha %q", strings.TrimSpace(string(marker)), want)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}

// TestOrchestratorSkillsPreserveEditFromBuildStampedCheckout keeps the
// operator's copy theirs in that same layout: resolving a source is not licence
// to overwrite a skill edited in place.
func TestOrchestratorSkillsPreserveEditFromBuildStampedCheckout(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := stampedSkillsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
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
		t.Fatalf("expected the operator's edit preserved, got %q", data)
	}
}

// TestOrchestratorSkillsReportUnresolvableSource locks the second half of the
// contract: when nothing resolves, the launch still succeeds and the operator is
// told once — a build that silently stops installing skills reads exactly like
// one where the skill had not changed.
func TestOrchestratorSkillsReportUnresolvableSource(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearSkillsOverride(t)
	exeDir := runFromBareLayout(t)
	// A checkout that is not on this machine, which is what a binary built
	// elsewhere carries.
	moved := filepath.Join(t.TempDir(), "checkout-that-moved", "erun-skills", "skills")
	stampSkillsSource(t, moved)

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("the orchestrator must still launch cleanly: %v", err)
	}
	notes := emits.events(appNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("expected exactly one report, got %+v", notes)
	}
	payload, ok := notes[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", notes[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	// The report has to be actionable on its own: what the build expected, where
	// the running binary looked instead, and both recoveries.
	for _, want := range []string{moved, exeDir, "ERUN_SKILLS_DIR", "build.sh"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("report does not name %q:\n%s", want, payload.Message)
		}
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "erun-orchestrate")); !os.IsNotExist(err) {
		t.Fatalf("nothing resolvable must install nothing, stat err = %v", err)
	}
	// Once: the condition belongs to the build, so a second launch repeats it in
	// the log only.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if got := emits.events(appNotificationEvent); len(got) != 1 {
		t.Fatalf("expected the report said once, got %+v", got)
	}
}

// TestHostSkillsSourceOverrideWinsOverBuildStamp keeps ERUN_SKILLS_DIR meaning
// "use exactly this": it beats the build stamp, and an empty directory installs
// nothing rather than falling back to a source the caller did not name.
func TestHostSkillsSourceOverrideWinsOverBuildStamp(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	stampedSkillsCheckout(t, "# stamped\n")
	override := t.TempDir()
	t.Setenv("ERUN_SKILLS_DIR", override)

	resolved, err := hostSkillsSource()
	if err != nil {
		t.Fatalf("an override must resolve: %v", err)
	}
	if resolved != override {
		t.Fatalf("source = %q, want the override %q", resolved, override)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "erun-orchestrate")); !os.IsNotExist(err) {
		t.Fatalf("an empty override must install nothing, stat err = %v", err)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("an honoured override is not a failure to report, got %+v", notes)
	}
}

// TestOrchestratorSkillsResolveFromCheckoutAroundExecutable keeps the layout
// that already worked working: a binary sitting inside a checkout resolves that
// checkout's skills even with no build stamp, which is the case a packaged
// tree — or a binary built by anything other than these scripts — relies on.
func TestOrchestratorSkillsResolveFromCheckoutAroundExecutable(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearSkillsOverride(t)
	stampSkillsSource(t, "")

	checkout := t.TempDir()
	skillDir := filepath.Join(checkout, "erun-skills", "skills", "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# from the checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Two levels down, the way a built binary sits under its checkout.
	exeDir := filepath.Join(checkout, "erun-ui", "bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := runningExecutable
	runningExecutable = func() (string, error) { return filepath.Join(exeDir, "erun-app"), nil }
	t.Cleanup(func() { runningExecutable = previous })

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installedOrchestrateSkill(t)); string(data) != "# from the checkout\n" {
		t.Fatalf("expected the surrounding checkout's skill installed, got %q", data)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}

// sessionStartHookEntry and sessionStartGroup decode one SessionStart hook
// block from settings.json, shared by the tests below.
type sessionStartHookEntry struct {
	Type    string `json:"type"`
	Command string `json:"command"`
}

type sessionStartGroup struct {
	Matcher string                  `json:"matcher"`
	Hooks   []sessionStartHookEntry `json:"hooks"`
}

// readSessionStartGroups decodes the SessionStart hook blocks currently
// written to settings.json at path, returning the raw bytes too so callers can
// include them in failure messages.
func readSessionStartGroups(t *testing.T, path string) ([]sessionStartGroup, []byte) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings.json: %v", err)
	}
	var settings struct {
		Hooks struct {
			SessionStart []sessionStartGroup `json:"SessionStart"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("unmarshal settings.json: %v\n%s", err, data)
	}
	return settings.Hooks.SessionStart, data
}

// sessionStartCommandCounts flattens every command across the given groups
// into a command -> occurrence-count map.
func sessionStartCommandCounts(groups []sessionStartGroup) map[string]int {
	seen := map[string]int{}
	for _, group := range groups {
		for _, hook := range group.Hooks {
			seen[hook.Command]++
		}
	}
	return seen
}

// assertSessionStartCommandsUnique fails if any SessionStart command appears
// more than once across all groups: two matcher blocks ("startup", "resume")
// carrying the same three commands would mean every SessionStart command,
// including the ~5.5KB contract injection, is installed (and so read into
// every session's context) twice.
func assertSessionStartCommandsUnique(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if count != 1 {
			t.Fatalf("SessionStart command installed %d times, want 1:\n%s\nfull settings:\n%s", count, cmd, data)
		}
	}
}

// assertSessionStartMatchersCover fails unless every source is matched by
// some group's matcher, tested the same way Claude Code itself resolves a
// SessionStart matcher: as a regex against the event's source.
func assertSessionStartMatchersCover(t *testing.T, groups []sessionStartGroup, data []byte, sources ...string) {
	t.Helper()
	for _, source := range sources {
		matched := false
		for _, group := range groups {
			re, err := regexp.Compile(group.Matcher)
			if err != nil {
				t.Fatalf("SessionStart matcher %q does not compile as a regex: %v", group.Matcher, err)
			}
			if re.MatchString(source) {
				matched = true
				break
			}
		}
		if !matched {
			t.Fatalf("no SessionStart matcher covers source %q:\n%s", source, data)
		}
	}
}

func TestOrchestratorSessionStartHookInjectsContract(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	groups, data := readSessionStartGroups(t, filepath.Join(dir, ".claude", "settings.json"))

	for _, group := range groups {
		if len(group.Hooks) == 0 || group.Hooks[0].Type != "command" {
			t.Fatalf("SessionStart matcher %q missing a command hook:\n%s", group.Matcher, data)
		}
		if !strings.Contains(group.Hooks[0].Command, "CLAUDE.md") {
			t.Fatalf("SessionStart command does not inject the contract (print CLAUDE.md):\n%s", group.Hooks[0].Command)
		}
	}
	assertSessionStartCommandsUnique(t, groups, data)
	assertSessionStartMatchersCover(t, groups, data, "startup", "resume", "clear", "compact")
}

// TestOrchestratorSessionStartHookPrunesEarlierDuplicatesAndPreservesForeignHooks
// seeds settings.json with the shape an earlier release left on disk -- two
// matcher blocks, each carrying the full three-command SessionStart payload --
// alongside an operator-owned SessionStart hook the installer has never seen.
// Installing on top of that must collapse the
// duplicate blocks down to one clean copy, leave the operator's own hook alone,
// and stay exactly that size across a second install (simulating a further
// desktop restart), never growing.
func TestOrchestratorSessionStartHookPrunesEarlierDuplicatesAndPreservesForeignHooks(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	dir := orchestratorsRoot()
	claudeDir := filepath.Join(dir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(claudeDir, "settings.json")

	staleContractCommand := orchestratorSkillHookCommand(dir)
	stale := map[string]any{
		"hooks": map[string]any{
			"SessionStart": []any{
				map[string]any{"matcher": "startup", "hooks": []any{
					map[string]any{"type": "command", "command": staleContractCommand},
				}},
				map[string]any{"matcher": "resume", "hooks": []any{
					map[string]any{"type": "command", "command": staleContractCommand},
				}},
				map[string]any{"matcher": "clear", "hooks": []any{
					map[string]any{"type": "command", "command": "echo operator-owned"},
				}},
			},
		},
	}
	raw, err := json.Marshal(stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	groupsAfterFirst, data := readSessionStartGroups(t, settingsPath)
	assertSessionStartContractCommandPrunedToOne(t, groupsAfterFirst, data)
	if !strings.Contains(string(data), "echo operator-owned") {
		t.Fatalf("expected the operator's own SessionStart hook preserved:\n%s", data)
	}

	// A further install (another desktop restart) against the now-clean file
	// must not grow the count.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace (second run): %v", err)
	}
	groupsAfterSecond, data2 := readSessionStartGroups(t, settingsPath)
	if got, want := sessionStartTotalCommands(groupsAfterSecond), sessionStartTotalCommands(groupsAfterFirst); got != want {
		t.Fatalf("SessionStart command count changed across a second install: got %d, want %d (unchanged):\n%s", got, want, data2)
	}
}

// sessionStartTotalCommands counts every hook entry across all groups.
func sessionStartTotalCommands(groups []sessionStartGroup) int {
	n := 0
	for _, group := range groups {
		n += len(group.Hooks)
	}
	return n
}

// assertSessionStartContractCommandPrunedToOne fails if the contract-injection
// command (identified by its fallback text) appears more than once.
func assertSessionStartContractCommandPrunedToOne(t *testing.T, groups []sessionStartGroup, data []byte) {
	t.Helper()
	for cmd, count := range sessionStartCommandCounts(groups) {
		if strings.Contains(cmd, orchestratorContractFallback) && count != 1 {
			t.Fatalf("expected the earlier duplicate contract-injection blocks pruned to one, found %d:\n%s", count, data)
		}
	}
}

// runOrchestratorSessionStartGroup runs every command in the SessionStart
// group whose matcher regex matches source, the same way Claude Code itself
// dispatches a SessionStart event, and returns their combined stdout. Fails
// the test if no group's matcher covers source at all.
func runOrchestratorSessionStartGroup(t *testing.T, shell string, groups []sessionStartGroup, data []byte, orchestratorID, source string) string {
	t.Helper()
	var out strings.Builder
	matched := false
	for _, group := range groups {
		re, err := regexp.Compile(group.Matcher)
		if err != nil {
			t.Fatalf("SessionStart matcher %q does not compile as a regex: %v", group.Matcher, err)
		}
		if !re.MatchString(source) {
			continue
		}
		matched = true
		for _, hook := range group.Hooks {
			cmd := exec.Command(shell, "-c", hook.Command)
			cmd.Stdin = strings.NewReader(`{"session_id":"post-` + source + `-session","hook_event_name":"SessionStart","source":"` + source + `"}`)
			cmd.Env = append(os.Environ(), "ERUN_ORCHESTRATOR_ID="+orchestratorID)
			stdout, err := cmd.Output()
			if err != nil {
				t.Fatalf("run SessionStart hook for source %q: %v", source, err)
			}
			out.Write(stdout)
		}
	}
	if !matched {
		t.Fatalf("no SessionStart matcher covers source %q:\n%s", source, data)
	}
	return out.String()
}

// assertRoleFilePreservedOnDisk fails unless rolePath still contains marker --
// the operator's own content must never be rewritten.
func assertRoleFilePreservedOnDisk(t *testing.T, rolePath, marker string) {
	t.Helper()
	got, err := os.ReadFile(rolePath)
	if err != nil {
		t.Fatalf("read role file: %v", err)
	}
	if !strings.Contains(string(got), marker) {
		t.Fatalf("expected the role file preserved on disk, got %q", got)
	}
}

// assertSessionStartReinjectsRoleAndContract runs the installed SessionStart
// hooks for each of sources against a real shell, the way Claude Code would
// dispatch them, and fails unless every one re-injects both the role marker
// and the shared contract into stdout.
func assertSessionStartReinjectsRoleAndContract(t *testing.T, shell, orchestratorID, roleMarker string, groups []sessionStartGroup, data []byte, sources ...string) {
	t.Helper()
	for _, source := range sources {
		injected := runOrchestratorSessionStartGroup(t, shell, groups, data, orchestratorID, source)
		if !strings.Contains(injected, roleMarker) {
			t.Fatalf("source %q did not re-inject the role file into context; got stdout:\n%s", source, injected)
		}
		if !strings.Contains(injected, "# Orchestrator working directory") {
			t.Fatalf("source %q did not re-inject the shared contract into context; got stdout:\n%s", source, injected)
		}
	}
}

// TestOrchestratorSessionStartHookReinjectsRoleFileOnClearAndCompact is the
// end-to-end reproduction from #1232: an orchestrator's standing role
// (CLAUDE.<id>.md) survives on disk across a /clear or a compaction, but
// nothing re-injected it into context because SessionStart's "clear" and
// "compact" sources were not registered alongside "startup"/"resume". This
// runs the actual installed hook commands, the way Claude Code would dispatch
// them for each source, and checks the role text is really printed to stdout
// rather than merely asserting the matcher string covers the source.
func TestOrchestratorSessionStartHookReinjectsRoleFileOnClearAndCompact(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no POSIX shell on this host")
	}
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())

	const orchestratorID = "erun-issues"
	const roleMarker = "STANDING ROLE MARKER: triage erun issues, never close without a reproduction"

	dir, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	// Seed a non-empty, operator-authored role file BEFORE the per-id ensure
	// call, matching the real sequence (the file is written once and never
	// rewritten): the operator's own content, not the placeholder seed, is
	// what must survive and be re-injected.
	rolePath := filepath.Join(dir, "CLAUDE."+orchestratorID+".md")
	if err := os.WriteFile(rolePath, []byte(roleMarker+"\n"), 0o644); err != nil {
		t.Fatalf("seed role file: %v", err)
	}
	if _, err := app.ensureOrchestratorWorkspaceFor(orchestratorID); err != nil {
		t.Fatalf("ensureOrchestratorWorkspaceFor: %v", err)
	}
	assertRoleFilePreservedOnDisk(t, rolePath, roleMarker)

	groups, data := readSessionStartGroups(t, filepath.Join(dir, ".claude", "settings.json"))
	assertSessionStartReinjectsRoleAndContract(t, shell, orchestratorID, roleMarker, groups, data, "clear", "compact")

	// The live-session record is the other half of #1232: a compaction forks
	// the transcript to a new session id, and the record must pick that id up
	// immediately from this same SessionStart firing rather than waiting for
	// the next turn-boundary hook.
	got, ok := readOrchestratorLiveSessionID(orchestratorID)
	if !ok || got != "post-compact-session" {
		t.Fatalf("expected the live-session record updated to the post-compact id, got %q ok=%v", got, ok)
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
	// PreToolUse is an event erun writes to as well now, so "preserved" has to
	// mean the operator's own hook is still there — not merely that the key is.
	preToolUse, _ := hooks["PreToolUse"].([]any)
	if !strings.Contains(string(data), "echo keep") {
		t.Fatalf("expected the operator's own PreToolUse hook preserved:\n%s", data)
	}
	if len(preToolUse) < 2 {
		t.Fatalf("expected erun's report merged alongside it, got %d blocks:\n%s", len(preToolUse), data)
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

// posixFallback splits a POSIX `primary || fallback` chain and returns the
// fallback half, failing the (sub)test if the chain shape is not there.
func posixFallback(t *testing.T, cmd string) string {
	t.Helper()
	parts := strings.SplitN(cmd, " || ", 2)
	if len(parts) != 2 {
		t.Fatalf("expected a primary || fallback chain, got %q", cmd)
	}
	return parts[1]
}

// TestBuildOrchestratorLaunchFallbackKeepsThePin locks down the crash-fallback
// fix: a pinned orchestrator whose primary launch fails must fall back to
// retrying its OWN conversation, never to a bare unpinned `claude`. An unpinned
// fallback is what the live-session hook then records as this orchestrator's
// conversation, silently swapping it onto an amnesiac session on every crash.
func TestBuildOrchestratorLaunchFallbackKeepsThePin(t *testing.T) {
	const sid = "6f7e9c2a-1b3d-4e5f-8a9b-000000000003"
	const prompt = "carry on"

	t.Run("existing session fallback retries resume", func(t *testing.T) {
		_, launch := buildOrchestratorLaunch("linux", sid, true, "", "", "")
		fallback := posixFallback(t, launch[len(launch)-1])
		if !strings.Contains(fallback, "--resume "+sid) {
			t.Fatalf("fallback for an existing session must retry --resume, got %q", fallback)
		}
	})

	t.Run("first open fallback retries the same create", func(t *testing.T) {
		// No conversation on disk yet: the fallback must retry the same
		// --session-id create rather than dropping to unpinned fresh.
		_, launch := buildOrchestratorLaunch("linux", sid, false, "", "", "")
		fallback := posixFallback(t, launch[len(launch)-1])
		if !strings.Contains(fallback, "--session-id "+sid) {
			t.Fatalf("first-open fallback must keep the pinned session id, got %q", fallback)
		}
	})

	t.Run("resume prompt chains through the pinned fallback", func(t *testing.T) {
		_, launch := buildOrchestratorLaunch("linux", sid, true, "", prompt, "")
		fallback := posixFallback(t, launch[len(launch)-1])
		if !strings.Contains(fallback, sid) || !strings.Contains(fallback, prompt) {
			t.Fatalf("resume-prompt fallback must keep both the pin and the prompt, got %q", fallback)
		}
	})

	t.Run("windows uses the LASTEXITCODE chain", func(t *testing.T) {
		_, launch := buildOrchestratorLaunch("windows", sid, true, "", "", "")
		cmd := launch[len(launch)-1]
		parts := strings.SplitN(cmd, "$LASTEXITCODE -ne 0", 2)
		if len(parts) != 2 || !strings.Contains(parts[1], sid) {
			t.Fatalf("windows fallback must keep the pinned session id, got %q", cmd)
		}
	})

	t.Run("transient launch keeps the unpinned fresh fallback", func(t *testing.T) {
		_, launch := buildOrchestratorLaunch("linux", "", false, "", "", "")
		fallback := posixFallback(t, launch[len(launch)-1])
		if !strings.HasPrefix(fallback, "claude"+orchestratorUltracodeFlag) {
			t.Fatalf("transient fallback should remain unpinned fresh, got %q", fallback)
		}
	})
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

// A prompt is ordinary operator task text — code spans, `$`, backslashes and
// quotes — and the harness has to receive it as one argument, unchanged. Two
// independent things break that, so this drives both: the positional prompt is
// consumed by the preceding multi-value --mcp-config unless option parsing ends
// first, and the host shell executes the metacharacters unless the value is
// quoted for the shell that re-parses the command line.
// An orchestrator is contracted to resolve ambiguity itself and carry a task to
// a verified end, so stopping to ask is a defect — and one asked while the
// operator is away stalls the work until they come back. The harness cannot ask
// if it does not have the tool, which makes that structural rather than a matter
// of the agent's own judgement about its instructions.
func TestBuildOrchestratorLaunchCannotStopToAsk(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		_, launch := buildOrchestratorLaunch(goos, "", false, "", "", "")
		if command := launch[len(launch)-1]; !strings.Contains(command, "--disallowedTools AskUserQuestion") {
			t.Fatalf("%s launch must deny the ask tool: %q", goos, command)
		}
	}
}

func TestBuildOrchestratorLaunchHandsThePromptOverVerbatim(t *testing.T) {
	const prompt = "Run `erun-ui/playwright/run.sh`, read $HOME, keep C:\\tmp\\x and \"quotes\" and 'apostrophes'"

	_, launch := buildOrchestratorLaunch("linux", "", false, prompt, "", "/cfg/orchestrator-mcp-erun.json")
	command := launch[len(launch)-1]
	if !strings.Contains(command, "--mcp-config '/cfg/orchestrator-mcp-erun.json' -- ") {
		t.Fatalf("the prompt must follow a -- that ends option parsing: %q", command)
	}
	if runtime.GOOS == "windows" {
		return
	}

	// Let a real POSIX shell parse what we composed and report the argv it
	// produces: anything it expands, executes or drops shows up here.
	dir := t.TempDir()
	stub := filepath.Join(dir, defaultAITool)
	if err := os.WriteFile(stub, []byte("#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done\n"), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	shellCmd := exec.Command("/bin/sh", "-c", command)
	shellCmd.Env = []string{"PATH=" + dir, "HOME=/expanded-so-the-quoting-leaked"}
	out, err := shellCmd.Output()
	if err != nil {
		t.Fatalf("run the composed command: %v", err)
	}
	argv := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if got := argv[len(argv)-1]; got != prompt {
		t.Fatalf("the prompt did not survive the shell:\n got %q\nwant %q\nargv %v", got, prompt, argv)
	}
}

// The same guarantee on the other host shell: PowerShell expands `$` and treats
// the backtick as its escape character inside double quotes, so the prompt and
// the config path are single-quoted there, with embedded apostrophes doubled.
func TestBuildOrchestratorLaunchQuotesThePromptForPowerShell(t *testing.T) {
	_, launch := buildOrchestratorLaunch("windows", "", false, "keep $HOME and `code` and it's fine", "", `C:\cfg\mcp.json`)
	command := launch[len(launch)-1]
	if !strings.Contains(command, "-- 'keep $HOME and `code` and it''s fine'") {
		t.Fatalf("windows prompt must be single-quoted with doubled apostrophes: %q", command)
	}
	if !strings.Contains(command, `--mcp-config 'C:\cfg\mcp.json'`) {
		t.Fatalf("windows mcp-config path must be single-quoted: %q", command)
	}
}

func TestInvestigateFailureRejectsEmptyReport(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	if _, err := app.InvestigateFailure("   ", "frs", "dev", 80, 24); err == nil {
		t.Fatal("expected an empty failure report to be rejected")
	}
}

// A restart hand-off names the conversation to continue, and the launch attaches
// to that one rather than re-deriving it from the orchestrator id — which is
// mutable and reusable, so several conversations answer to it.
func TestStartOrchestratorWithResumeAttachesToTheNamedConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	var launchedConversation, launchedPrompt string
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(sessionID, _, resumePrompt, _ string) (string, []string, error) {
			launchedConversation, launchedPrompt = sessionID, resumePrompt
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestratorWithResume(created.ID, "conversation-that-asked", "finish the task", 80, 24); err != nil {
		t.Fatalf("StartOrchestratorWithResume failed: %v", err)
	}

	if launchedConversation != "conversation-that-asked" {
		t.Fatalf("expected the named conversation to be resumed, got %q", launchedConversation)
	}
	if launchedPrompt != "finish the task" {
		t.Fatalf("expected the task to be handed to it, got %q", launchedPrompt)
	}

	// Without one, the orchestrator's own pinned conversation still answers.
	app.stopOrchestratorSession(created.ID)
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	if launchedConversation != orchestratorSessionID(created.ID) {
		t.Fatalf("expected the pinned conversation without a hand-off, got %q", launchedConversation)
	}
}

// TestOrchestratorReconnectRefusedGuards is the standalone unit test for the
// two bounds tryReconnect's orchestrator-specific guard exists to enforce: a
// clean exit (no reason — the operator quit the TUI, not a crash) is always
// refused, and a torn-down registration is refused even carrying a real crash
// reason — which is what makes an operator's Stop refuse its own respawn.
func TestOrchestratorReconnectRefusedGuards(t *testing.T) {
	app := NewApp(erunUIDeps{})
	id := "agent"
	key := orchestratorSessionKey(id)
	managed := &managedTerminal{key: key, kind: sessionKindOrchestrator}
	app.orchestrators[id] = &orchestratorSession{id: id}
	app.sessions[key] = managed

	if !app.orchestratorReconnectRefused(managed, "") {
		t.Fatal("a clean exit (empty reason) must refuse respawn")
	}
	if app.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a crash with an intact registration must not be refused")
	}

	delete(app.orchestrators, id)
	delete(app.sessions, key)
	if !app.orchestratorReconnectRefused(managed, "exit status 1") {
		t.Fatal("a torn-down registration must refuse respawn, so Stop cannot be undone")
	}
}

// TestOrchestratorRespawnsAfterCrashIntoTheSameConversation is the end-to-end
// path for the crash-recovery half of #1260: a session that exits non-zero is
// relaunched automatically, into the SAME conversation, carrying a prompt that
// tells it to carry on rather than idling.
func TestOrchestratorRespawnsAfterCrashIntoTheSameConversation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	var conversations, prompts []string
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(sessionID, _, resumePrompt, _ string) (string, []string, error) {
			mu.Lock()
			conversations = append(conversations, sessionID)
			prompts = append(prompts, resumePrompt)
			mu.Unlock()
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	first.waitErr = errors.New("exit status 1")
	mu.Unlock()
	_ = first.Close()

	waitForSessionCount(t, &mu, &sessions, 2, 2*time.Second)

	mu.Lock()
	defer mu.Unlock()
	if len(conversations) != 2 {
		t.Fatalf("expected two launches (initial + respawn), got %d: %v", len(conversations), conversations)
	}
	if conversations[0] != conversations[1] {
		t.Fatalf("respawn must resume the SAME conversation, got %q then %q", conversations[0], conversations[1])
	}
	if prompts[1] == "" || !strings.Contains(prompts[1], "carry") {
		t.Fatalf("respawn must carry a prompt telling it to continue, got %q", prompts[1])
	}
}

// TestOrchestratorCleanExitDoesNotRespawn is the counterpart: an exit with no
// reason (Wait returned nil — the operator quit the TUI from inside) must end
// the session rather than relaunch it.
func TestOrchestratorCleanExitDoesNotRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	mu.Lock()
	first := sessions[0]
	mu.Unlock()
	_ = first.Close() // waitErr is nil: a clean exit

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(emits.events(terminalExitEvent)) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if len(emits.events(terminalExitEvent)) == 0 {
		t.Fatal("expected the clean exit to finalize the session")
	}
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("a clean exit must not respawn, got %d sessions", got)
	}
}

// TestStopOrchestratorRefusesItsOwnRespawn locks the bound end to end: an
// operator's Stop must never be undone by an in-flight crash respawn.
func TestStopOrchestratorRefusesItsOwnRespawn(t *testing.T) {
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)

	var mu sync.Mutex
	var sessions []*stubTerminalSession
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			session := newStubTerminalSession()
			mu.Lock()
			sessions = append(sessions, session)
			mu.Unlock()
			return session, nil
		},
		resolveOrchestratorLaunch: func(string, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}

	if err := app.StopOrchestrator(created.ID); err != nil {
		t.Fatalf("StopOrchestrator failed: %v", err)
	}

	time.Sleep(200 * time.Millisecond)
	mu.Lock()
	got := len(sessions)
	mu.Unlock()
	if got != 1 {
		t.Fatalf("Stop must not be undone by a respawn, got %d sessions", got)
	}
	if info, ok := app.runningOrchestratorInfo(created.ID); ok {
		t.Fatalf("expected no running session after Stop, got %+v", info)
	}
}
