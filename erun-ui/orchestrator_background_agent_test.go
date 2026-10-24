package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/command/orchestrator"
)

type agentLaunches struct {
	mu       sync.Mutex
	agentIDs []string
}

func (l *agentLaunches) all() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.agentIDs...)
}

func agentTestApp(t *testing.T, agents *fakeClaudeAgents) (*App, *agentLaunches, *capturedEmits) {
	t.Helper()
	app := orchestratorTestApp(t)
	t.Cleanup(func() { app.shutdown(context.Background()) })
	launches := &agentLaunches{}
	app.deps.claudeAgents = agents
	app.deps.resolveOrchestratorLaunch = func(_ orchestrator.OrchestratorLaunch, agentID, _, _ string) (string, []string, error) {
		launches.mu.Lock()
		launches.agentIDs = append(launches.agentIDs, agentID)
		launches.mu.Unlock()
		if agentID == "" {
			return "claude-stub", nil, nil
		}
		shell, args := orchestratorAttachCommand("linux", agentID)
		return shell, args, nil
	}
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	return app, launches, emits
}

func startAgentOrchestrator(t *testing.T, app *App, name string) string {
	t.Helper()
	created, err := app.CreateOrchestrator(name, []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, err, "CreateOrchestrator")
	_, err = app.StartOrchestrator(created.ID, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")
	return created.ID
}

func notificationMessages(emits *capturedEmits) string {
	var out []string
	for _, event := range emits.events(appNotificationEvent) {
		if payload, ok := event.(appNotificationPayload); ok {
			out = append(out, payload.Message)
		}
	}
	return strings.Join(out, "\n")
}

func argValue(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestReopeningAfterAClearAttachesToTheAgentsCurrentConversation(t *testing.T) {
	agents := newFakeClaudeAgents()
	app, launches, _ := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")

	created := agents.named(id)
	if len(created) != 1 {
		t.Fatalf("expected one background agent named %q, got %+v", id, created)
	}
	agentID := created[0].ID
	agents.clear(agentID, "b4196e75-0000-4000-8000-000000000000")

	app.shutdown(context.Background())
	reopened := NewApp(app.deps)
	t.Cleanup(func() { reopened.shutdown(context.Background()) })
	_, err := reopened.StartOrchestrator(id, 80, 24)
	mustNoErr(t, err, "StartOrchestrator after the desktop restarted")

	if all := launches.all(); len(all) != 2 || all[0] != agentID || all[1] != agentID {
		t.Fatalf("expected both desktops to attach to %s, got %v", agentID, all)
	}
	if got := agents.named(id); len(got) != 1 || got[0].ID != agentID || got[0].SessionID != "b4196e75-0000-4000-8000-000000000000" {
		t.Fatalf("expected the same agent, still on its post-clear conversation, got %+v", got)
	}
	requireCreatedOnceNamingNoConversation(t, agents)
}

func requireCreatedOnceNamingNoConversation(t *testing.T, agents *fakeClaudeAgents) {
	t.Helper()
	agents.mu.Lock()
	creates := agents.creates
	agents.mu.Unlock()
	if len(creates) != 1 {
		t.Fatalf("expected the agent created once and reattached after, got %d creates", len(creates))
	}
	for _, flag := range []string{"--resume", "--session-id", "--continue"} {
		if strings.Contains(strings.Join(creates[0], " "), flag) {
			t.Fatalf("a new orchestrator's agent must not name a conversation (%s): %v", flag, creates[0])
		}
	}
}

func TestAnAgentIsFoundByItsExactNameOnly(t *testing.T) {
	agents := newFakeClaudeAgents(
		claudeBackgroundAgent{ID: "11111111", PID: 1, Kind: "background", Name: "erun-ui", SessionID: "s1"},
		claudeBackgroundAgent{ID: "22222222", PID: 2, Kind: "background", Name: "my-erun", SessionID: "s2"},
	)
	app, launches, _ := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")

	mine := agents.named(id)
	if len(mine) != 1 {
		t.Fatalf("expected an agent of its own, got %+v", mine)
	}
	if all := launches.all(); len(all) != 1 || all[0] != mine[0].ID {
		t.Fatalf("expected the desktop to attach to its own agent %s, got %v", mine[0].ID, all)
	}
	if len(agents.named("erun-ui")) != 1 || len(agents.named("my-erun")) != 1 {
		t.Fatal("agents with merely similar names must be left alone")
	}
}

func TestTwoAgentsWithTheSameNameAreReportedNotGuessed(t *testing.T) {
	agents := newFakeClaudeAgents(
		claudeBackgroundAgent{ID: "11111111", PID: 1, Kind: "background", Name: "erun", SessionID: "s1"},
		claudeBackgroundAgent{ID: "22222222", PID: 2, Kind: "background", Name: "erun", SessionID: "s2"},
	)
	app, launches, _ := agentTestApp(t, agents)
	created, err := app.CreateOrchestrator("erun", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, err, "CreateOrchestrator")

	_, err = app.StartOrchestrator(created.ID, 80, 24)
	if err == nil {
		t.Fatal("expected two same-named agents to be refused")
	}
	for _, want := range []string{"11111111", "22222222", "claude rm <id>"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("expected the refusal to name %q, got %v", want, err)
		}
	}
	if len(launches.all()) != 0 || len(agents.named("erun")) != 2 {
		t.Fatal("a refused open must attach to nothing and change nothing")
	}
}

func TestAnAgentErunDidNotCreateIsRecreatedOnItsConversation(t *testing.T) {
	agents := newFakeClaudeAgents(
		claudeBackgroundAgent{ID: "07d0ffec", PID: 81118, Kind: "background", Name: "erun", SessionID: "186d2cd9-013a-4e0e-98cf-b76294806741"},
	)
	app, launches, emits := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")

	got := agents.named(id)
	if len(got) != 1 || got[0].ID == "07d0ffec" {
		t.Fatalf("expected the foreign agent replaced, got %+v", got)
	}
	if got[0].SessionID != "186d2cd9-013a-4e0e-98cf-b76294806741" {
		t.Fatalf("expected the replacement to continue the same conversation, got %q", got[0].SessionID)
	}
	if all := launches.all(); len(all) != 1 || all[0] != got[0].ID {
		t.Fatalf("expected the desktop to attach to the replacement, got %v", all)
	}
	if msg := notificationMessages(emits); !strings.Contains(msg, "Recreating the Claude background agent for erun (07d0ffec)") ||
		!strings.Contains(msg, "not started by erun") || !strings.Contains(msg, "same conversation") {
		t.Fatalf("expected the operator told why the agent was replaced, got %q", msg)
	}
}

func TestMovingEffortToMaxRecreatesTheAgentOnItsCurrentConversation(t *testing.T) {
	agents := newFakeClaudeAgents()
	app, launches, emits := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")
	first := agents.named(id)[0]
	agents.clear(first.ID, "b4196e75-0000-4000-8000-000000000000")

	_, err := orchestrator.SetOrchestratorEffort(core.Context{}, app.deps.store, model.SetOrchestratorEffortParams{OrchestratorID: id, Effort: "max"})
	mustNoErr(t, err, "SetOrchestratorEffort")
	_, err = app.RestartOrchestrator(id, 80, 24)
	mustNoErr(t, err, "RestartOrchestrator")

	got := agents.named(id)
	if len(got) != 1 || got[0].ID == first.ID {
		t.Fatalf("expected the agent recreated, got %+v", got)
	}
	created := lastAgentCreate(t, app)
	if argValue(created, "--effort") != "max" || argValue(created, "--resume") != "b4196e75-0000-4000-8000-000000000000" {
		t.Fatalf("expected a max-effort agent continuing the post-clear conversation, got %v", created)
	}
	if all := launches.all(); all[len(all)-1] != got[0].ID {
		t.Fatalf("expected the desktop to attach to the recreated agent, got %v", all)
	}
	if msg := notificationMessages(emits); !strings.Contains(msg, "launch arguments changed") {
		t.Fatalf("expected the operator told the agent was recreated, got %q", msg)
	}
}

func TestARestartAppliesSettingsWithoutRecreatingTheAgent(t *testing.T) {
	agents := newFakeClaudeAgents()
	app, _, emits := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")
	agentID := agents.named(id)[0].ID

	_, err := orchestrator.SetOrchestratorModel(core.Context{}, app.deps.store, model.SetOrchestratorModelParams{OrchestratorID: id, Model: "sonnet"})
	mustNoErr(t, err, "SetOrchestratorModel")
	restarted, err := app.RestartOrchestrator(id, 80, 24)
	mustNoErr(t, err, "RestartOrchestrator")

	if got := agents.named(id); len(got) != 1 || got[0].ID != agentID {
		t.Fatalf("expected the same agent kept, got %+v", got)
	}
	agents.mu.Lock()
	stops, creates := append([]string(nil), agents.stops...), len(agents.creates)
	agents.mu.Unlock()
	if creates != 1 || len(stops) != 1 || stops[0] != agentID {
		t.Fatalf("expected the agent stopped once and never recreated, got stops=%v creates=%d", stops, creates)
	}
	if settings, _ := agentSettingsOf(t, id); settings["model"] != "sonnet" {
		t.Fatalf("expected the restart to write the new model, got %v", settings)
	}
	if restarted.LaunchSettingsChanged {
		t.Fatalf("a restarted agent runs on its settings, got %+v", restarted)
	}
	if msg := notificationMessages(emits); strings.Contains(msg, "Recreating") {
		t.Fatalf("a settings-only restart must not recreate the agent, got %q", msg)
	}
}

func TestReattachingARunningAgentReportsTheScopeItStartedWith(t *testing.T) {
	agents := newFakeClaudeAgents()
	app, _, _ := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")

	_, err := app.UpdateOrchestrator(id, "erun", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}, {Tenant: "frs", Environment: "laptop"}}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, err, "UpdateOrchestrator")
	app.stopOrchestratorSession(id)
	reopened, err := app.StartOrchestrator(id, 80, 24)
	mustNoErr(t, err, "StartOrchestrator")
	if !reopened.RestartRequired {
		t.Fatalf("expected the reattached agent to need a restart for its new scope, got %+v", reopened)
	}
	restarted, err := app.RestartOrchestrator(id, 80, 24)
	mustNoErr(t, err, "RestartOrchestrator")
	if restarted.RestartRequired {
		t.Fatalf("expected the restarted agent to run on its scope, got %+v", restarted)
	}
}

func TestStopKeepsTheAgentAndDeleteRemovesIt(t *testing.T) {
	agents := newFakeClaudeAgents()
	app, _, _ := agentTestApp(t, agents)
	id := startAgentOrchestrator(t, app, "erun")
	agentID := agents.named(id)[0].ID

	mustNoErr(t, app.StopOrchestrator(id), "StopOrchestrator")
	if got := agents.named(id); len(got) != 1 || got[0].running() {
		t.Fatalf("expected the agent kept and stopped, got %+v", got)
	}

	mustNoErr(t, app.DeleteOrchestrator(id), "DeleteOrchestrator")
	if got := agents.named(id); len(got) != 0 {
		t.Fatalf("expected the agent removed, got %+v", got)
	}
	agents.mu.Lock()
	removes := append([]string(nil), agents.removes...)
	agents.mu.Unlock()
	if len(removes) != 1 || removes[0] != agentID {
		t.Fatalf("expected %s removed, got %v", agentID, removes)
	}
	if _, ok := readOrchestratorAgentRecord(id); ok {
		t.Fatal("expected the agent record removed with the orchestrator")
	}
}

func TestAnUntrustedWorkspaceIsReportedWithItsRemedy(t *testing.T) {
	agents := newFakeClaudeAgents()
	_, err := parseClaudeAgentCreated("Workspace not trusted. Run `claude` in /Users/op/orchestrators once and accept the trust prompt, then retry.\n",
		errors.New("exit status 1"), "/Users/op/orchestrators")
	agents.createErr = err
	app, launches, _ := agentTestApp(t, agents)
	created, cerr := app.CreateOrchestrator("erun", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
	mustNoErr(t, cerr, "CreateOrchestrator")

	_, err = app.StartOrchestrator(created.ID, 80, 24)
	if !errors.Is(err, errOrchestratorWorkspaceUntrusted) {
		t.Fatalf("expected the untrusted workspace reported, got %v", err)
	}
	if !strings.Contains(err.Error(), "Run `claude` once in /Users/op/orchestrators, accept the trust prompt") {
		t.Fatalf("expected the remedy in the refusal, got %v", err)
	}
	if len(launches.all()) != 0 {
		t.Fatal("an agent that never started must not be attached to")
	}
}

func TestTheCreatedAgentIsReadFromAnyLineOfTheOutput(t *testing.T) {
	out := "Starting the background service…\nbackgrounded · e4505af2 · erun\n  claude agents             list sessions\n"
	id, err := parseClaudeAgentCreated(out, nil, "/o")
	if err != nil || id != "e4505af2" {
		t.Fatalf("expected e4505af2, got %q (%v)", id, err)
	}
	if _, err := parseClaudeAgentCreated("something else\n", nil, "/o"); err == nil || !strings.Contains(err.Error(), "did not name the agent") {
		t.Fatalf("expected an output naming no agent to be refused, got %v", err)
	}
}

func TestTheAgentListingKeepsBackgroundAgentsOnly(t *testing.T) {
	agents, err := parseClaudeBackgroundAgents([]byte(`[
		{"pid":81118,"id":"07d0ffec","kind":"background","sessionId":"186d2cd9","name":"erun"},
		{"pid":97488,"kind":"interactive","sessionId":"edd6a939","name":"erun"},
		{"id":"33cfe4e8","kind":"background","sessionId":"1f645b6d","name":"probe"}
	]`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(agents) != 2 || !agents[0].running() || agents[1].running() {
		t.Fatalf("expected two background agents, the second stopped, got %+v", agents)
	}
}

func TestAFirstAgentContinuesTheConversationOfTheEarlierScheme(t *testing.T) {
	t.Run("derived", func(t *testing.T) {
		agents := newFakeClaudeAgents()
		app, _, _ := agentTestApp(t, agents)
		created, err := app.CreateOrchestrator("erun", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
		mustNoErr(t, err, "CreateOrchestrator")
		derived := uuid.NewSHA1(legacyOrchestratorSessionNamespace, []byte(created.ID)).String()
		stageOrchestratorConversation(t, derived)

		_, err = app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")
		if got := argValue(lastAgentCreate(t, app), "--resume"); got != derived {
			t.Fatalf("expected the first agent to continue %s, got %q", derived, got)
		}
	})
	t.Run("attached", func(t *testing.T) {
		agents := newFakeClaudeAgents()
		app, _, _ := agentTestApp(t, agents)
		created, err := app.CreateOrchestrator("erun", []orchestratorEnvInput{{Tenant: "frs", Environment: "dev"}}, nil, "", orchestratorLaunchInput{})
		mustNoErr(t, err, "CreateOrchestrator")
		const attached = "3bde477f-6a1c-4a2e-9f0d-2c5b8e7a1d34"
		stageOrchestratorConversation(t, attached)
		stageOrchestratorConversation(t, uuid.NewSHA1(legacyOrchestratorSessionNamespace, []byte(created.ID)).String())
		mustNoErr(t, writeOpenOrchestrators(app.deps.orchestratorOpenPath, []orchestratorOpenEntry{{OrchestratorID: created.ID, AttachedConversationID: attached}}), "seed open record")

		_, err = app.StartOrchestrator(created.ID, 80, 24)
		mustNoErr(t, err, "StartOrchestrator")
		if got := argValue(lastAgentCreate(t, app), "--resume"); got != attached {
			t.Fatalf("expected the attached conversation to win, got %q", got)
		}
	})
}
