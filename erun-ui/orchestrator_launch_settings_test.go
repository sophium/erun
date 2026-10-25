package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/ai"
	"github.com/sophium/erun/erun-common/command/orchestrator"
	"github.com/sophium/erun/erun-ui/internal/terminal"
)

const (
	defaultOrchestratorFlags = ` --settings '{"ultracode":true}' --disallowedTools AskUserQuestion --model opus`
	testGatewayURL           = "https://gateway.example.test"
	testGatewayToken         = "sk-gateway-test-secret"
)

type orchestratorLaunchRecorder struct {
	mu       sync.Mutex
	launches []orchestrator.OrchestratorLaunch
	args     [][]string
	envs     [][]string
	token    string
	tokenErr error
	tokenFor []*configpkg.OpenRouterConfig
}

func (r *orchestratorLaunchRecorder) last(t *testing.T) (orchestrator.OrchestratorLaunch, []string, []string) {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.launches) == 0 || len(r.envs) == 0 {
		t.Fatal("no orchestrator session was launched")
	}
	return r.launches[len(r.launches)-1], r.args[len(r.args)-1], r.envs[len(r.envs)-1]
}

func (r *orchestratorLaunchRecorder) started() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.envs)
}

func (r *orchestratorLaunchRecorder) setCredential(token string, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.token, r.tokenErr = token, err
}

func launchSettingsTestApp(t *testing.T) (*App, *orchestratorLaunchRecorder) {
	t.Helper()
	app := orchestratorTestApp(t)
	rec := &orchestratorLaunchRecorder{token: testGatewayToken}
	app.deps.resolveOrchestratorLaunch = func(launch orchestrator.OrchestratorLaunch, agentID, initialPrompt, mcpConfigPath string) (string, []string, error) {
		shell, args := buildOrchestratorLaunch("linux", launch, initialPrompt, mcpConfigPath)
		if agentID != "" {
			shell, args = orchestratorAttachCommand("linux", agentID)
		}
		rec.mu.Lock()
		rec.launches = append(rec.launches, launch)
		rec.mu.Unlock()
		return shell, args, nil
	}
	app.deps.startTerminal = func(p terminal.StartParams) (terminal.PTY, error) {
		rec.mu.Lock()
		rec.args = append(rec.args, append([]string{p.Executable}, p.Args...))
		rec.envs = append(rec.envs, append([]string(nil), p.Env...))
		rec.mu.Unlock()
		return newStubTerminalSession(), nil
	}
	app.deps.resolveGatewayCredential = func(gateway *configpkg.OpenRouterConfig) (string, error) {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		rec.tokenFor = append(rec.tokenFor, gateway)
		return rec.token, rec.tokenErr
	}
	return app, rec
}

func stageGatewayCatalog(app *App) {
	stubStore(app).config.OpenRouter = &configpkg.OpenRouterConfig{
		BaseURL:      testGatewayURL,
		DefaultModel: "acme/coder",
		Models: []configpkg.OpenRouterModel{
			{ID: "acme/coder", Context: 200000},
			{ID: "acme/thinker", RequiresReasoningEcho: true},
		},
	}
}

func editStoredOrchestrator(app *App, id string, edit func(*configpkg.OrchestratorConfig)) {
	config := stubStore(app).config
	for i := range config.Orchestrators {
		if config.Orchestrators[i].ID == id {
			edit(&config.Orchestrators[i])
		}
	}
}

func requireDefaultOrchestratorLaunch(t *testing.T, launch orchestrator.OrchestratorLaunch) {
	t.Helper()
	if launch.Model != "opus" || launch.EffortFlags != ` --settings '{"ultracode":true}'` || launch.Gateway != nil {
		t.Fatalf("expected the default launch, got %+v", launch)
	}
}

func launchSettingsOfInfo(info orchestratorInfo) orchestratorLaunchInput {
	return orchestratorLaunchInput{UseGateway: info.UseGateway, Model: info.Model, Effort: info.Effort}
}

func requireLaunchSettingsRow(t *testing.T, label string, info orchestratorInfo, want orchestratorLaunchInput, changed bool) {
	t.Helper()
	if launchSettingsOfInfo(info) != want || info.LaunchSettingsChanged != changed {
		t.Fatalf("%s: expected settings %+v with LaunchSettingsChanged=%v, got %+v", label, want, changed, info)
	}
}

func requireLoneLaunchSettingsRow(t *testing.T, app *App, label string, want orchestratorLaunchInput, changed bool) orchestratorInfo {
	t.Helper()
	listed := app.ListOrchestrators()
	if len(listed) != 1 {
		t.Fatalf("%s: expected one orchestrator, got %+v", label, listed)
	}
	requireLaunchSettingsRow(t, label, listed[0], want, changed)
	return listed[0]
}

func requireStoredLaunchSettings(t *testing.T, app *App, label string, want orchestratorLaunchInput) {
	t.Helper()
	stored := mustLoadRootConfig(t, app).Orchestrators
	if len(stored) != 1 || orchestratorLaunchSettingsOf(stored[0]) != want {
		t.Fatalf("%s: expected %+v on disk, got %+v", label, want, stored)
	}
}

func mustCreateOrchestratorWithLaunch(t *testing.T, app *App, launch orchestratorLaunchInput) orchestratorInfo {
	t.Helper()
	info, err := app.CreateOrchestrator("agent", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", launch)
	mustNoErr(t, err, "CreateOrchestrator")
	return info
}

func agentSettingsOf(t *testing.T, id string) (map[string]any, map[string]string) {
	t.Helper()
	path := orchestratorAgentSettingsPath(id)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read agent settings: %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("expected the agent settings private to the operator, got %v (err %v)", info.Mode().Perm(), err)
	}
	var settings map[string]any
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatalf("decode agent settings: %v\n%s", err, data)
	}
	env := map[string]string{}
	if raw, ok := settings["env"].(map[string]any); ok {
		for key, value := range raw {
			env[key], _ = value.(string)
		}
	}
	return settings, env
}

func lastAgentCreate(t *testing.T, app *App) []string {
	t.Helper()
	fake := fakeAgentsOf(t, app)
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if len(fake.creates) == 0 {
		t.Fatal("no background agent was created")
	}
	return fake.creates[len(fake.creates)-1]
}

func requireAgentEnv(t *testing.T, env map[string]string, want map[string]string) {
	t.Helper()
	for key, value := range want {
		if got, ok := env[key]; !ok || got != value {
			t.Fatalf("expected the agent's settings env to carry %s=%q, got %v", key, value, env)
		}
	}
}

func requireNoGatewayEnv(t *testing.T, env map[string]string) {
	t.Helper()
	for key := range env {
		if strings.HasPrefix(key, "ANTHROPIC_") {
			t.Fatalf("an orchestrator off the gateway must add no ANTHROPIC_ env, got %v", env)
		}
	}
}

func TestATransientSessionWithNoSettingsLaunchesExactlyAsBefore(t *testing.T) {
	launch := defaultOrchestratorLaunch()
	if !slices.Equal(launch.Env, []string{"CLAUDE_CODE_SUBAGENT_MODEL=opus"}) {
		t.Fatalf("expected the default env to be exactly the subagent model, got %q", launch.Env)
	}
	for _, goos := range []string{"linux", "windows"} {
		_, args := buildOrchestratorLaunch(goos, launch, "", "")
		if got := args[len(args)-1]; got != "claude"+defaultOrchestratorFlags {
			t.Fatalf("%s:\n got %q\nwant %q", goos, got, "claude"+defaultOrchestratorFlags)
		}
	}
}

func TestAnOrchestratorWithNoSettingsLaunchesOnTheDefaults(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{})
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	settings, env := agentSettingsOf(t, created.ID)
	if settings["model"] != "opus" || settings["ultracode"] != true || settings["effortLevel"] != nil {
		t.Fatalf("expected ultracode Opus, got %v", settings)
	}
	requireAgentEnv(t, env, map[string]string{"CLAUDE_CODE_SUBAGENT_MODEL": "opus"})
	requireNoGatewayEnv(t, env)
	if args := strings.Join(lastAgentCreate(t, app), " "); strings.Contains(args, "--effort") || strings.Contains(args, "--model") {
		t.Fatalf("model and effort belong in the settings a restart re-reads, not the launch arguments: %q", args)
	}
	if len(rec.tokenFor) != 0 {
		t.Fatal("an orchestrator off the gateway must not read the gateway credential")
	}
}

func TestEveryEffortLevelReachesTheLaunchFlags(t *testing.T) {
	for _, level := range ai.ClaudeEffortLevels() {
		launch, err := orchestrator.ResolveOrchestratorLaunch(configpkg.OrchestratorConfig{Model: "sonnet", Effort: level}, nil)
		if err != nil {
			t.Fatalf("effort %q: %v", level, err)
		}
		want := " --effort " + level + orchestratorNoAskFlag + " --model sonnet"
		if level == "ultracode" {
			want = ` --settings '{"ultracode":true}'` + orchestratorNoAskFlag + " --model sonnet"
		}
		for _, goos := range []string{"linux", "windows"} {
			_, args := buildOrchestratorLaunch(goos, launch, "", "")
			cmd := args[len(args)-1]
			if strings.Count(cmd, want) != 1 {
				t.Fatalf("%s effort %q: expected %q once, got %q", goos, level, want, cmd)
			}
			if level != "ultracode" && strings.Contains(cmd, "ultracode") {
				t.Fatalf("%s effort %q must not also launch ultracode: %q", goos, level, cmd)
			}
		}
	}
}

func TestAConfiguredModelReachesTheLaunchAndItsSubagents(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{Model: "sonnet", Effort: "high"})
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	launch, _, _ := rec.last(t)
	if launch.Model != "sonnet" || launch.Effort != "high" {
		t.Fatalf("expected the configured model and effort, got %+v", launch)
	}
	settings, env := agentSettingsOf(t, created.ID)
	if settings["model"] != "sonnet" || settings["effortLevel"] != "high" {
		t.Fatalf("expected the configured model and effort in the agent's settings, got %v", settings)
	}
	if env["CLAUDE_CODE_SUBAGENT_MODEL"] != "sonnet" {
		t.Fatalf("expected the subagent model to follow, got %v", env)
	}
}

// A command line is visible to every process on the host.
func TestAGatewayLaunchDeliversItsCredentialOnlyThroughTheEnvironment(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	stageGatewayCatalog(app)
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{UseGateway: true})
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	launch, args, _ := rec.last(t)
	if launch.Model != "acme/coder" {
		t.Fatalf("expected the catalog's default model through the gateway, got %q", launch.Model)
	}
	// A background agent does not inherit its starter's environment.
	settings, env := agentSettingsOf(t, created.ID)
	requireAgentEnv(t, env, map[string]string{
		"ANTHROPIC_BASE_URL":                         testGatewayURL,
		"ANTHROPIC_API_KEY":                          "",
		"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1",
		"CLAUDE_CODE_MAX_CONTEXT_TOKENS":             "200000",
		"CLAUDE_CODE_SUBAGENT_MODEL":                 "acme/coder",
		"ANTHROPIC_AUTH_TOKEN":                       testGatewayToken,
	})
	if settings["model"] != "acme/coder" {
		t.Fatalf("expected the gateway model in the agent's settings, got %v", settings)
	}
	if cmd := strings.Join(append(args, lastAgentCreate(t, app)...), " "); strings.Contains(cmd, testGatewayToken) {
		t.Fatalf("the gateway credential leaked into a command line: %q", cmd)
	}
	if len(rec.tokenFor) != 1 || rec.tokenFor[0].Endpoint() != testGatewayURL {
		t.Fatalf("expected the credential resolved once for the catalog's gateway, got %+v", rec.tokenFor)
	}
}

func TestAGatewayLaunchWithoutACredentialRefusesToStart(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	stageGatewayCatalog(app)
	rec.setCredential("", errors.New("no gateway credential: nothing is saved under \"claude-gateway\""))
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{UseGateway: true})

	_, err := app.StartOrchestrator(created.ID, 80, 24)
	if err == nil {
		t.Fatal("expected a gateway launch with no credential to be refused")
	}
	for _, want := range []string{"no gateway credential", "save the gateway key in the desktop's ERun settings"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the refusal to carry %q, got %v", want, err)
		}
	}
	if rec.started() != 0 {
		t.Fatal("a refused start must not launch a session")
	}
	if listed := app.ListOrchestrators(); len(listed) != 1 || listed[0].Status != "stopped" {
		t.Fatalf("expected the orchestrator still stopped, got %+v", listed)
	}
}

func TestAGatewayWithNoCatalogRefusesToStartAndKeepsTheRunningSession(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{})
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	editStoredOrchestrator(app, created.ID, func(o *configpkg.OrchestratorConfig) { o.UseGateway = true })

	_, err := app.RestartOrchestrator(created.ID, 80, 24)
	if err == nil || !strings.Contains(err.Error(), "no gateway catalog is configured") ||
		!strings.Contains(err.Error(), "configure the gateway catalog") {
		t.Fatalf("expected the restart refused with the cause and its remedy, got %v", err)
	}
	if rec.started() != 1 {
		t.Fatalf("a refused restart must not launch a session, got %d launches", rec.started())
	}
	if listed := app.ListOrchestrators(); len(listed) != 1 || listed[0].Status != "running" {
		t.Fatalf("a refused restart must leave the running session alone, got %+v", listed)
	}

	app.stopOrchestratorSession(created.ID)
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err == nil || !strings.Contains(err.Error(), "no gateway catalog is configured") {
		t.Fatalf("expected the start refused, got %v", err)
	}
}

func TestChangingLaunchSettingsOnARunningOrchestratorAsksForARestart(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{})
	started, err := app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")
	requireLaunchSettingsRow(t, "fresh launch", started, orchestratorLaunchInput{}, false)

	sonnet := orchestratorLaunchInput{Model: "sonnet"}
	updated, err := app.UpdateOrchestrator(created.ID, "agent", devEnvs, nil, "", sonnet)
	mustNoErr(t, err, "UpdateOrchestrator")
	requireLaunchSettingsRow(t, "save while running", updated, sonnet, true)
	if updated.RestartRequired {
		t.Fatalf("a launch-settings edit is not a scope change, got %+v", updated)
	}
	if row := requireLoneLaunchSettingsRow(t, app, "polled after save", sonnet, true); row.Status != "running" {
		t.Fatalf("expected the session still running, got %+v", row)
	}
	again, err := app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator on a running orchestrator")
	requireLaunchSettingsRow(t, "running snapshot", again, sonnet, true)

	restarted, err := app.RestartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "RestartOrchestrator")
	requireLaunchSettingsRow(t, "after restart", restarted, sonnet, false)
	if launch, _, _ := rec.last(t); launch.Model != "sonnet" {
		t.Fatalf("expected the restart to launch the configured model, got %q", launch.Model)
	}

	_, err = orchestrator.SetOrchestratorEffort(core.Context{}, app.deps.store, model.SetOrchestratorEffortParams{
		OrchestratorID: created.ID, Effort: "low",
	})
	mustNoErr(t, err, "SetOrchestratorEffort")
	requireLoneLaunchSettingsRow(t, app, "polled after a CLI write", orchestratorLaunchInput{Model: "sonnet", Effort: "low"}, true)
}

func TestACrashRespawnKeepsTheLaunchItContinues(t *testing.T) {
	app, rec := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	stageGatewayCatalog(app)
	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{UseGateway: true, Model: "acme/coder", Effort: "max"})
	if _, err := app.StartOrchestrator(created.ID, 80, 24); err != nil {
		t.Fatalf("StartOrchestrator failed: %v", err)
	}
	editStoredOrchestrator(app, created.ID, func(o *configpkg.OrchestratorConfig) {
		o.UseGateway, o.Model, o.Effort = false, "haiku", "low"
	})
	rec.setCredential("sk-rotated", nil)

	l, unlock := lockTerminalForTest(app)
	respawn := l.Session(terminal.OrchestratorSessionKey(created.ID)).Respawn
	unlock()
	if respawn == nil {
		t.Fatal("expected a persisted orchestrator to carry a crash respawn")
	}
	if _, err := respawn(); err != nil {
		t.Fatalf("respawn failed: %v", err)
	}
	launch, _, _ := rec.last(t)
	if launch.Model != "acme/coder" || launch.Effort != "max" || launch.Gateway == nil {
		t.Fatalf("expected the respawn to keep the original launch, got %+v", launch)
	}
	settings, env := agentSettingsOf(t, created.ID)
	if settings["model"] != "acme/coder" {
		t.Fatalf("expected the respawn to keep the original model, got %v", settings)
	}
	requireAgentEnv(t, env, map[string]string{"ANTHROPIC_AUTH_TOKEN": testGatewayToken, "CLAUDE_CODE_SUBAGENT_MODEL": "acme/coder"})
	if args := strings.Join(lastAgentCreate(t, app), " "); !strings.Contains(args, "--effort max") {
		t.Fatalf("max effort is a launch argument of the agent, got %q", args)
	}
}

func TestLaunchSettingsPersistAndSurviveAnUnrelatedEdit(t *testing.T) {
	app, _ := launchSettingsTestApp(t)
	defer app.shutdown(context.Background())
	stageGatewayCatalog(app)
	devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
	want := orchestratorLaunchInput{UseGateway: true, Model: "acme/coder", Effort: "max"}

	created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{UseGateway: true, Model: " acme/coder ", Effort: "max"})
	requireLaunchSettingsRow(t, "create", created, want, false)
	requireStoredLaunchSettings(t, app, "after create", want)

	listed := requireLoneLaunchSettingsRow(t, app, "listed after create", want, false)
	renamed, err := app.UpdateOrchestrator(created.ID, "renamed", devEnvs, nil, listed.Alias, launchSettingsOfInfo(listed))
	mustNoErr(t, err, "UpdateOrchestrator")
	requireLaunchSettingsRow(t, "rename", renamed, want, false)
	requireStoredLaunchSettings(t, app, "after rename", want)

	_, err = orchestrator.SetOrchestratorModel(core.Context{}, app.deps.store, model.SetOrchestratorModelParams{
		OrchestratorID: created.ID, Model: "",
	})
	mustNoErr(t, err, "SetOrchestratorModel")
	cleared := orchestratorLaunchInput{UseGateway: true, Effort: "max"}
	listed = requireLoneLaunchSettingsRow(t, app, "listed after a CLI write", cleared, false)
	_, err = app.UpdateOrchestrator(created.ID, "renamed again", devEnvs, nil, "", launchSettingsOfInfo(listed))
	mustNoErr(t, err, "UpdateOrchestrator")
	requireStoredLaunchSettings(t, app, "after a dialog save over a CLI write", cleared)
}

func TestInvalidLaunchSettingsAreRefusedBySave(t *testing.T) {
	cases := []struct {
		name    string
		catalog bool
		launch  orchestratorLaunchInput
		want    string
	}{
		{"unknown effort", false, orchestratorLaunchInput{Effort: "extreme"}, "invalid effort"},
		{"flag-shaped model", false, orchestratorLaunchInput{Model: "-p"}, "invalid model"},
		{"shell syntax in the model", false, orchestratorLaunchInput{Model: "opus; touch /tmp/x"}, "invalid model"},
		{"gateway with no catalog", false, orchestratorLaunchInput{UseGateway: true}, "configure the gateway catalog"},
		{"model the gateway cannot drive", true, orchestratorLaunchInput{UseGateway: true, Model: "acme/thinker"}, "cannot be driven through the gateway"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app, _ := launchSettingsTestApp(t)
			defer app.shutdown(context.Background())
			if tc.catalog {
				stageGatewayCatalog(app)
			}
			devEnvs := []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}
			if _, err := app.CreateOrchestrator("agent", devEnvs, nil, "", tc.launch); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected create refused with %q, got %v", tc.want, err)
			}
			if stored := mustLoadRootConfig(t, app).Orchestrators; len(stored) != 0 {
				t.Fatalf("a refused create persisted %+v", stored)
			}
			created := mustCreateOrchestratorWithLaunch(t, app, orchestratorLaunchInput{})
			if _, err := app.UpdateOrchestrator(created.ID, "agent", devEnvs, nil, "", tc.launch); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected update refused with %q, got %v", tc.want, err)
			}
			if stored := orchestratorLaunchSettingsOf(mustLoadRootConfig(t, app).Orchestrators[0]); stored != (orchestratorLaunchInput{}) {
				t.Fatalf("a refused update persisted %+v", stored)
			}
		})
	}
}
