package main

import (
	"strings"
	"testing"
)

// newTestAppForDeployQueue builds a minimal App with an in-memory deploy
// queue suitable for unit tests. No real terminal sessions are spawned.
func newTestAppForDeployQueue(t *testing.T) *App {
	t.Helper()
	app := &App{
		sessions: make(map[string]*managedTerminal),
	}
	app.deployQueue = newDeployQueueStore(nil, nil, nil)
	return app
}

func TestStartDeployTrackingRegistersAndLocksMatchingSessions(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev", serial: 1, kind: sessionKindOpen}
	aiSession := &managedTerminal{selection: selection, key: "ai\x00team\x00dev", serial: 2, kind: sessionKindAI}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 3, kind: sessionKindLocal}
	otherSelection := uiSelection{Tenant: "other", Environment: "dev", Version: "1.0.0"}
	unrelated := &managedTerminal{selection: otherSelection, key: "env\x00other\x00dev", serial: 4, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	app.sessions[aiSession.key] = aiSession
	app.sessions[localSession.key] = localSession
	app.sessions[unrelated.key] = unrelated

	entry, joined := app.startDeployTracking(selection, localSession.serial)
	if joined {
		t.Fatalf("expected fresh tracking, got joined")
	}
	if entry.Tenant != "team" || entry.Environment != "dev" {
		t.Fatalf("entry selection drift: %+v", entry)
	}
	if envSession.lockedByDeploy != entry.ID {
		t.Fatalf("env session not locked: lockedByDeploy=%q want=%q", envSession.lockedByDeploy, entry.ID)
	}
	if aiSession.lockedByDeploy != entry.ID {
		t.Fatalf("ai session not locked: lockedByDeploy=%q want=%q", aiSession.lockedByDeploy, entry.ID)
	}
	if localSession.lockedByDeploy != "" {
		t.Fatalf("local session unexpectedly locked: %q (the local tab is where the deploy is being driven from)", localSession.lockedByDeploy)
	}
	if unrelated.lockedByDeploy != "" {
		t.Fatalf("unrelated tenant locked: %q", unrelated.lockedByDeploy)
	}
}

func TestStartDeployTrackingDuplicateLocksOnlyTriggeringSession(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	first := &managedTerminal{selection: selection, key: "env\x00team\x00dev\x000", serial: 1, kind: sessionKindOpen}
	app.sessions[first.key] = first
	entry, _ := app.startDeployTracking(selection, first.serial)
	// Now a second (later) ai session shows up — only it should get locked
	// by the duplicate path, not redundantly re-lock first.
	second := &managedTerminal{selection: selection, key: "ai\x00team\x00dev\x000", serial: 2, kind: sessionKindAI}
	app.sessions[second.key] = second
	dup, joined := app.startDeployTracking(selection, second.serial)
	if !joined {
		t.Fatalf("expected join on duplicate start")
	}
	if dup.ID != entry.ID {
		t.Fatalf("duplicate returned different entry ID: %s vs %s", dup.ID, entry.ID)
	}
	if second.lockedByDeploy != entry.ID {
		t.Fatalf("second session not locked: %q", second.lockedByDeploy)
	}
}

func TestFinishDeployTrackingUnlocksMatchingSessions(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	envSession := &managedTerminal{selection: selection, key: "env\x00team\x00dev\x000", serial: 1, kind: sessionKindOpen}
	app.sessions[envSession.key] = envSession
	app.startDeployTracking(selection, 0)
	if envSession.lockedByDeploy == "" {
		t.Fatal("session not locked at start")
	}
	app.finishDeployTracking(selection, deployQueueStatusSucceeded, "")
	if envSession.lockedByDeploy != "" {
		t.Fatalf("session still locked after finish: %q", envSession.lockedByDeploy)
	}
}

func TestDeployTraceLineHandlerTransitionsOnDeployedAndFailed(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	app.startDeployTracking(selection, 0)
	handler := newDeployTraceLineHandler(app, selection)
	handler("    waiting for helm rollout (timeout 2m0s)...")
	if _, ok := app.deployQueue.findActive("team", "dev"); !ok {
		t.Fatal("non-trace line should not finish the deploy")
	}
	handler("==> Deployed team/dev 1.0.0 in 12s")
	if _, ok := app.deployQueue.findActive("team", "dev"); ok {
		t.Fatal("deploy should be finished after ==> Deployed")
	}
	all := app.deployQueue.list()
	if len(all) != 1 || all[0].Status != deployQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}

	app2 := newTestAppForDeployQueue(t)
	app2.startDeployTracking(selection, 0)
	handler2 := newDeployTraceLineHandler(app2, selection)
	handler2("Error: UPGRADE FAILED: timeout")
	handler2("==> Deploy failed after 2m0s")
	all2 := app2.deployQueue.list()
	if len(all2) != 1 || all2[0].Status != deployQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all2)
	}
	if !strings.Contains(all2[0].Error, "Deploy failed") && !strings.Contains(all2[0].Error, "UPGRADE FAILED") {
		t.Fatalf("error not captured: %q", all2[0].Error)
	}
}

func TestDeployTraceLineHandlerTransitionsOnSkipping(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	app.startDeployTracking(selection, 0)
	handler := newDeployTraceLineHandler(app, selection)
	handler("==> Skipping team/dev (identical deploy already in progress)")
	all := app.deployQueue.list()
	if len(all) != 1 || all[0].Status != deployQueueStatusSkipped {
		t.Fatalf("expected one skipped entry, got %+v", all)
	}
}

func TestNamespaceForTenantEnv(t *testing.T) {
	cases := []struct {
		tenant, environment, want string
	}{
		{"team", "dev", "team-dev"},
		{"team", "", "team"},
		{"", "dev", "dev"},
		{"", "", ""},
	}
	for _, c := range cases {
		if got := namespaceForTenantEnv(c.tenant, c.environment); got != c.want {
			t.Fatalf("namespaceForTenantEnv(%q,%q) = %q, want %q", c.tenant, c.environment, got, c.want)
		}
	}
}

func TestReleaseNameForTenant(t *testing.T) {
	if got := releaseNameForTenant("team"); got != "team-devops" {
		t.Fatalf("got %q, want team-devops", got)
	}
	if got := releaseNameForTenant("  spaced  "); got != "spaced-devops" {
		t.Fatalf("got %q, want spaced-devops", got)
	}
	if got := releaseNameForTenant(""); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
