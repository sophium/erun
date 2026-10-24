package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

// newOrchestratorStubStore backs the store with a persistent in-memory root
// config plus one env of each agent type — remote-agent frs/dev, reviewed in a
// mirror, and local-agent frs/laptop, reviewed in the worktree at laptopRepoPath
// — plus a runtime env, which has no worktree to review and no agent to delegate
// to and so is not orchestratable.
func newOrchestratorStubStore(laptopRepoPath string) stubUIStore {
	return stubUIStore{
		config: &configpkg.ERunConfig{},
		tenants: map[string]configpkg.TenantConfig{
			"frs": {Name: "frs", DefaultEnvironment: "dev"},
		},
		envs: map[string]configpkg.EnvConfig{
			"frs/dev":     {Name: "dev", Type: configpkg.EnvironmentTypeRemoteAgent, KubernetesContext: "ctx"},
			"frs/laptop":  {Name: "laptop", Type: configpkg.EnvironmentTypeLocalAgent, LocalRepoPath: laptopRepoPath},
			"frs/runtime": {Name: "runtime", Type: configpkg.EnvironmentTypeRuntime},
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
	// Same default for the reusable agents, for the same reason.
	t.Setenv("ERUN_AGENTS_DIR", t.TempDir())
	laptopRepo := t.TempDir()
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(laptopRepo),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		canReachMCPEndpoint:         reachable,
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
		// Never the real secret store: a test that launches through the gateway
		// stages its own credential.
		resolveGatewayCredential: func(*configpkg.OpenRouterConfig) (string, error) {
			return "", errors.New("no gateway credential staged by this test")
		},
	})
	// Stage failure reports inside the test's own directory. Left at the default
	// they land in the shared host temp dir, which is how a suite that spawns
	// nothing still left two reports per run behind — and made a handful of real
	// failures read as dozens of spawned agents.
	app.investigations.reportDir = t.TempDir()
	return app, laptopRepo
}

// TestCreateOrchestratorLinksARuntimeEnvWithTheRuntimeRole is the link-time
// half of scope item 2: a runtime env may be linked when its role is
// runtime, and the persisted link carries no review directory — the
// operate-role link resolves no review directory, per scope item 3, asserted
// on the resolved value rather than the absence of an error.
func TestCreateOrchestratorLinksARuntimeEnvWithTheRuntimeRole(t *testing.T) {
	app, _ := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	info, err := app.CreateOrchestrator("runtime operator", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "runtime", Role: configpkg.OrchestratorEnvRoleRuntime},
	}, nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}
	if len(info.Environments) != 1 {
		t.Fatalf("expected one linked environment, got %+v", info.Environments)
	}
	linked := info.Environments[0]
	if linked.Role != configpkg.OrchestratorEnvRoleRuntime {
		t.Fatalf("expected the runtime role to persist, got %+v", linked)
	}
	if linked.Directory != "" {
		t.Fatalf("expected no review directory for a runtime-role link, got %+v", linked)
	}
}

// TestCreateOrchestratorRejectsARuntimeEnvWithoutTheRuntimeRole locks scope
// item 6: linking a runtime env as code or build (or leaving the role
// undeclared) must still be refused, narrowing the gate rather than opening
// it, with the same reason orchestratorIneligibilityReason gives.
func TestCreateOrchestratorRejectsARuntimeEnvWithoutTheRuntimeRole(t *testing.T) {
	app, _ := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	for _, role := range []configpkg.OrchestratorEnvRole{configpkg.OrchestratorEnvRoleCode, configpkg.OrchestratorEnvRoleBuild, ""} {
		_, err := app.CreateOrchestrator("runtime operator", []orchestratorEnvInput{
			{Tenant: "frs", Environment: "runtime", Role: role},
		}, nil, "", orchestratorLaunchInput{})
		if err == nil {
			t.Fatalf("expected linking the runtime env with role %q to be refused", role)
		}
		if !strings.Contains(err.Error(), "no worktree") {
			t.Fatalf("expected the refusal to name the concrete gap, got %v", err)
		}
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
	}, nil, "", orchestratorLaunchInput{})
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
	store := stubStore(app)
	store.envs["frs/gone"] = configpkg.EnvConfig{Name: "gone", Type: configpkg.EnvironmentTypeLocalAgent, LocalRepoPath: missing}
	store.envs["frs/unset"] = configpkg.EnvConfig{Name: "unset", Type: configpkg.EnvironmentTypeLocalAgent}

	if _, err := app.CreateOrchestrator("gone", []orchestratorEnvInput{{Tenant: "frs", Environment: "gone"}}, nil, "", orchestratorLaunchInput{}); err == nil {
		t.Fatal("expected a local-agent env whose worktree is absent to be rejected")
	}
	if _, err := app.CreateOrchestrator("unset", []orchestratorEnvInput{{Tenant: "frs", Environment: "unset"}}, nil, "", orchestratorLaunchInput{}); err == nil {
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
	store := stubStore(app)
	store.envs["frs/laptop"] = configpkg.EnvConfig{Name: "laptop", Type: configpkg.EnvironmentTypeLocalAgent, LocalRepoPath: moved}
	chosenMirror := filepath.Join(t.TempDir(), "my-mirror")

	refreshed := app.refreshLinkedEnvDirectories([]configpkg.OrchestratorEnvConfig{
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

	if _, err := app.CreateOrchestrator("no envs", nil, nil, "", orchestratorLaunchInput{}); err == nil {
		t.Fatal("expected an orchestrator with no linked environments to be rejected")
	}

	info, err := app.CreateOrchestrator(
		"validation agent",
		[]orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}},
		nil, "", orchestratorLaunchInput{})
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

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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
		resolveOrchestratorLaunch: func(orchestrator.OrchestratorLaunch, string, string, string) (string, []string, error) {
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
	})
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
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
		nil, "", orchestratorLaunchInput{})
	if err != nil {
		t.Fatalf("CreateOrchestrator failed: %v", err)
	}

	updated, err := app.UpdateOrchestrator(
		created.ID,
		"agent renamed",
		[]orchestratorEnvInput{
			{Tenant: "frs", Environment: "dev", Directory: t.TempDir()},
		},
		nil, "", orchestratorLaunchInput{})
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

// mcpConfigServerKeys reads back the wired MCP server map for id and returns
// its keys, so a test can assert exactly which environments a live session's
// toolset covers rather than trusting the in-memory scope alone (erun#1319).
func mcpConfigServerKeys(t *testing.T, id string) []string {
	t.Helper()
	data, err := os.ReadFile(orchestratorMCPConfigPath(id))
	if err != nil {
		t.Fatalf("read orchestrator MCP config for %s: %v", id, err)
	}
	var config orchestratorMCPConfig
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("unmarshal orchestrator MCP config for %s: %v", id, err)
	}
	keys := make([]string, 0, len(config.MCPServers))
	for key := range config.MCPServers {
		keys = append(keys, key)
	}
	return keys
}

// requireOnlyOrchestratorMCPKey fails unless id's on-disk MCP config wires
// exactly wantKey, so a test can assert what a live session can actually
// reach rather than trusting the in-memory scope alone (erun#1319).
func requireOnlyOrchestratorMCPKey(t *testing.T, id, wantKey, label string) {
	t.Helper()
	if keys := mcpConfigServerKeys(t, id); len(keys) != 1 || keys[0] != wantKey {
		t.Fatalf("%s: expected the session wired for %s only, got %v", label, wantKey, keys)
	}
}

// requireLoneOrchestratorRestartRequired fails unless ListOrchestrators
// reports exactly one orchestrator whose RestartRequired flag matches want.
func requireLoneOrchestratorRestartRequired(t *testing.T, app *App, want bool, label string) {
	t.Helper()
	listed := app.ListOrchestrators()
	if len(listed) != 1 || listed[0].RestartRequired != want {
		t.Fatalf("%s: expected one orchestrator with RestartRequired=%v, got %+v", label, want, listed)
	}
}

// TestUpdateOrchestratorOnALiveSessionLeavesItsToolsetStale is the red-then-
// green case for erun#1319: re-scoping a running orchestrator changes what it
// is allowed to touch but the live session — its --mcp-config was resolved at
// launch and cannot be rewired in place — keeps whatever it was spawned with
// until it is restarted. Both defect directions are asserted on disk, not just
// against in-memory state: the unlinked env's server entry must still be
// there (RED: it stays fully tool-reachable) and the newly linked env's must
// not be (RED: it has no tools at all). The fix is UpdateOrchestrator no
// longer reporting "running" as if the save took effect: it flags
// RestartRequired, and ListOrchestrators keeps reporting it on every poll
// until a restart actually re-wires the session (GREEN).
func TestUpdateOrchestratorOnALiveSessionLeavesItsToolsetStale(t *testing.T) {
	// Pin the executable seam, for the same reason
	// TestWriteOrchestratorMCPConfigCarriesSkipsEvenWhenNothingWired does:
	// StartOrchestrator resolves the erun executable before it writes the
	// per-orchestrator MCP config, so on a host without one it writes no config
	// at all and the on-disk assertions below read a file that was never
	// created. The build's own test stage has no erun on PATH.
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))

	app, laptopRepo := orchestratorTestAppWithLocalRepo(t)
	defer app.shutdown(context.Background())

	created, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, err, "CreateOrchestrator")
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")
	if started.RestartRequired {
		t.Fatalf("expected a freshly spawned session to need no restart, got %+v", started)
	}
	requireOnlyOrchestratorMCPKey(t, created.ID, "frs-dev", "before re-scope")

	// Re-scope to a different environment while the session is still running.
	updated, err := app.UpdateOrchestrator(created.ID, "agent", []orchestratorEnvInput{
		{Tenant: "frs", Environment: "laptop", Directory: laptopRepo},
	}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, err, "UpdateOrchestrator")
	if updated.Status != "running" {
		t.Fatalf("expected the live session to still be reported running, got %+v", updated)
	}
	if !updated.RestartRequired {
		t.Fatalf("expected UpdateOrchestrator to flag a restart as required for a live scope change, got %+v", updated)
	}

	// RED: the live session's actual MCP config, read back from disk, has not
	// moved — frs/dev (unlinked) is still wired and frs/laptop (newly linked)
	// has no tools at all. Quoted here so the defect is visible, not asserted
	// away.
	requireOnlyOrchestratorMCPKey(t, created.ID, "frs-dev", "RED: after re-scope, before restart")
	// The same mismatch must keep showing up on every subsequent poll, not
	// just in UpdateOrchestrator's own return value.
	requireLoneOrchestratorRestartRequired(t, app, true, "RED: polled after re-scope")

	// GREEN: restarting is the only thing that re-wires the session, and it
	// resolves the flag along with the actual toolset.
	restarted, err := app.RestartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "RestartOrchestrator")
	if restarted.RestartRequired {
		t.Fatalf("expected a fresh spawn to clear RestartRequired, got %+v", restarted)
	}
	requireOnlyOrchestratorMCPKey(t, created.ID, "frs-laptop", "GREEN: after restart")
	requireLoneOrchestratorRestartRequired(t, app, false, "GREEN: polled after restart")
}

func TestInvestigateFailureSpawnsTransientTenantScopedOrchestrator(t *testing.T) {
	var seededPrompt string
	var seededLaunch orchestrator.OrchestratorLaunch
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("HOME", home)
	app := NewApp(erunUIDeps{
		store: newOrchestratorStubStore(t.TempDir()),
		startTerminal: func(startTerminalSessionParams) (terminalSession, error) {
			return newStubTerminalSession(), nil
		},
		resolveOrchestratorLaunch: func(launch orchestrator.OrchestratorLaunch, _, prompt, _ string) (string, []string, error) {
			seededPrompt, seededLaunch = prompt, launch
			return "claude-stub", nil, nil
		},
		runOrchestratorLabelCommand: stubOrchestratorClaimLabel,
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
	// A transient investigator has no persisted settings: it launches on the
	// fixed defaults whatever any orchestrator is configured with.
	requireDefaultOrchestratorLaunch(t, seededLaunch)
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
	// Launching again refreshes stale instructions in the same shared workspace.
	if err := os.WriteFile(filepath.Join(dir, "CLAUDE.md"), []byte("stale instructions\n"), 0o644); err != nil {
		t.Fatalf("seed stale instructions: %v", err)
	}
	again, err := app.ensureOrchestratorWorkspace()
	if err != nil {
		t.Fatalf("refresh orchestrator workspace: %v", err)
	}
	if again != dir {
		t.Fatalf("expected one shared workspace, got %q then %q", dir, again)
	}

	data, readErr := os.ReadFile(filepath.Join(dir, "CLAUDE.md"))
	if readErr != nil {
		t.Fatalf("read CLAUDE.md: %v", readErr)
	}
	if string(data) != orchestratorClaudeMd {
		t.Fatal("provisioned orchestrator instructions differ from the canonical template")
	}
	// The shared CLAUDE.md is generic — no per-orchestrator "Linked environments" list.
	if strings.Contains(string(data), "## Linked environments") {
		t.Fatalf("expected no per-orchestrator env list in the shared CLAUDE.md:\n%s", data)
	}
}

func TestInvestigateFailureRejectsEmptyReport(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	if _, err := app.InvestigateFailure("   ", "frs", "dev", 80, 24); err == nil {
		t.Fatal("expected an empty failure report to be rejected")
	}
}
