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

func TestDeployTraceLineHandlerAutoRegistersOnDeployingLine(t *testing.T) {
	// Regression: a deploy kicked off in the ERun tab (or anywhere outside
	// the desktop's Deploy button) prints `==> Deploying tenant/env version`
	// to its PTY but never calls StartDeploySession. The trace handler must
	// auto-register an entry from the printed line so the queue drawer sees
	// every deploy regardless of which tab it was started in.
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local", KubernetesContext: "orbstack"}
	handler := newDeployTraceLineHandler(app, selection)
	handler("==> Deploying erun/local 1.0.51-snapshot-20260510080136")
	entry, ok := app.deployQueue.findActive("erun", "local")
	if !ok {
		t.Fatal("expected deploy auto-registered from trace line")
	}
	if entry.Version != "1.0.51-snapshot-20260510080136" {
		t.Fatalf("version = %q, want 1.0.51-snapshot-20260510080136", entry.Version)
	}
	if entry.Release != "erun-devops" {
		t.Fatalf("release = %q, want erun-devops", entry.Release)
	}
	if entry.Namespace != "erun-local" {
		t.Fatalf("namespace = %q, want erun-local", entry.Namespace)
	}
	if entry.KubernetesContext != "orbstack" {
		t.Fatalf("context = %q, want orbstack", entry.KubernetesContext)
	}

	// Versionless deploy line (e.g. local snapshot deploy without explicit
	// --version) still registers, just with empty Version.
	app2 := newTestAppForDeployQueue(t)
	handler2 := newDeployTraceLineHandler(app2, uiSelection{Tenant: "team", Environment: "dev"})
	handler2("==> Deploying team/dev")
	if _, ok := app2.deployQueue.findActive("team", "dev"); !ok {
		t.Fatal("versionless deploy line did not register entry")
	}
}

func TestDeployTraceLineHandlerDoesNotDoubleRegister(t *testing.T) {
	app := newTestAppForDeployQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}
	app.startDeployTracking(selection, 0)
	first, _ := app.deployQueue.findActive("erun", "local")

	handler := newDeployTraceLineHandler(app, selection)
	handler("==> Deploying erun/local 1.0.0")
	second, _ := app.deployQueue.findActive("erun", "local")
	if first.ID != second.ID {
		t.Fatalf("trace line registered a duplicate entry: %s vs %s", first.ID, second.ID)
	}
}

func TestNewAppDoesNotPersistWithoutExplicitPath(t *testing.T) {
	// Regression: an earlier draft auto-resolved the persistence path inside
	// NewApp via os.UserConfigDir. Tests that exercised StartDeploySession
	// (e.g. the existing TestStartDeploySession_* fixtures using
	// "team-busy/dev-busy" and "erun/remote") wrote running entries into
	// the developer's real ~/Library/Application Support/ERun/deploy_queue.json.
	// Persistence path now flows through erunUIDeps so production opts in
	// from main.go and tests never touch real disk.
	app := NewApp(erunUIDeps{})
	if app.deployQueue == nil {
		t.Fatal("deployQueue not initialized")
	}
	app.deployQueue.start(deployQueueEntry{Tenant: "leak", Environment: "dev", Version: "1", Release: "leak-devops"})
}
