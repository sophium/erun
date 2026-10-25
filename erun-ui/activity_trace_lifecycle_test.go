package main

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Doctor runs piped into the Local shell with no PTY exit; trace lines are its only completion signal.
func TestActivityTraceLineHandlerStartsAndFinishesDoctor(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Doctor team/dev")
	entry, ok := app.activityQueue.findActiveByCommand("doctor", "team", "dev")
	if !ok {
		t.Fatal("expected doctor entry to be active after ==> Doctor")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Doctor done team/dev")
	if _, ok := app.activityQueue.findActiveByCommand("doctor", "team", "dev"); ok {
		t.Fatal("expected doctor entry to be finished after ==> Doctor done")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesDoctorOnFailure(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Doctor team/dev")
	handler("==> Doctor failed team/dev: kubectl not reachable")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if all[0].Error != "kubectl not reachable" {
		t.Fatalf("expected parsed reason, got %q", all[0].Error)
	}
}

// Locking would block doctor's interactive prompts.
func TestStartDoctorFromTraceDoesNotLockTerminal(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 1, kind: sessionKindLocal}
	app.sessions[localSession.key] = localSession
	app.startDoctorFromTrace(selection, "team", "dev")
	if localSession.lockedByActivity != "" {
		t.Fatalf("expected doctor to not lock terminal, got %q", localSession.lockedByActivity)
	}
}

func TestDoctorCompletedEventRecordsLastRunOutcome(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Doctor team/dev")
	handler("==> Doctor failed team/dev: kubectl not reachable")

	events := emits.events(uimodel.DoctorCompletedEvent)
	if len(events) != 1 {
		t.Fatalf("doctor-completed emitted %d times, want exactly 1", len(events))
	}
	payload, ok := events[0].(uiDoctorCompletedPayload)
	if !ok {
		t.Fatalf("doctor-completed payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "team" || payload.Environment != "dev" {
		t.Fatalf("payload selection = %+v, want team/dev", payload)
	}
	if payload.Success {
		t.Fatalf("expected success=false, got %+v", payload)
	}
	if payload.Message != "kubectl not reachable" {
		t.Fatalf("expected parsed message, got %q", payload.Message)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesSSHDInit(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> SSHD init team/dev")
	entry, ok := app.activityQueue.findActiveByCommand("sshd-init", "team", "dev")
	if !ok {
		t.Fatal("expected sshd-init entry to be active after ==> SSHD init")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> SSHD init done team/dev")
	if _, ok := app.activityQueue.findActiveByCommand("sshd-init", "team", "dev"); ok {
		t.Fatal("expected sshd-init entry to be finished after ==> SSHD init done")
	}
	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesSSHDInitOnFailure(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> SSHD init team/dev")
	handler("==> SSHD init failed team/dev: sync remote authorized_keys from key.pub: exit status 1")

	all := app.activityQueue.list()
	if len(all) != 1 || all[0].Status != activityQueueStatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if all[0].Error != "sync remote authorized_keys from key.pub: exit status 1" {
		t.Fatalf("expected parsed reason, got %q", all[0].Error)
	}
}

func TestStartSSHDInitFromTraceLocksTerminals(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 1, kind: sessionKindLocal}
	app.sessions[localSession.key] = localSession
	app.startSSHDInitFromTrace(selection, "team", "dev")
	if _, ok := app.activityQueue.findActiveByCommand("sshd-init", "team", "dev"); !ok {
		t.Fatal("expected sshd-init entry to be active")
	}
}

func TestSSHDInitCompletedEventRecordsLastRunOutcome(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> SSHD init team/dev")
	handler("==> SSHD init failed team/dev: kubectl not reachable")

	events := emits.events(uimodel.SSHDInitCompletedEvent)
	if len(events) != 1 {
		t.Fatalf("sshd-init-completed emitted %d times, want exactly 1", len(events))
	}
	payload, ok := events[0].(uiSSHDInitCompletedPayload)
	if !ok {
		t.Fatalf("sshd-init-completed payload has unexpected type %T", events[0])
	}
	if payload.Tenant != "team" || payload.Environment != "dev" {
		t.Fatalf("payload selection = %+v, want team/dev", payload)
	}
	if payload.Success {
		t.Fatalf("expected success=false, got %+v", payload)
	}
	if payload.Message != "kubectl not reachable" {
		t.Fatalf("expected parsed message, got %q", payload.Message)
	}
}

func TestStartCommandFromTraceDoesNotLockTerminal(t *testing.T) {
	// Locking would freeze the tab showing their output.
	for _, command := range []string{"build", "release", "push"} {
		app := newTestAppForActivityQueue(t)
		selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
		envSession := &managedTerminal{selection: selection, key: "env\x00erun\x00local", serial: 1, kind: sessionKindOpen}
		app.sessions[envSession.key] = envSession
		app.startCommandFromTrace(selection, command)
		if envSession.lockedByActivity != "" {
			t.Fatalf("expected %s to not lock terminal, got %q", command, envSession.lockedByActivity)
		}
	}
}

// Only the Local session may own init: honoring a remote open's re-printed init
// lines would trigger a deploy→reopen loop.
func TestActivityTraceLineHandlerIgnoresInitFromNonLocalSession(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}

	openHandler := newActivityTraceLineHandler(app, selection, sessionKindOpen)
	openHandler("==> Initializing erun/local")
	openHandler("==> Initialized erun/local")
	if _, ok := app.activityQueue.findActiveByCommand("init", "erun", "local"); ok {
		t.Fatal("open-session init trace must not register an init activity")
	}
	if got := app.activityQueue.list(); len(got) != 0 {
		t.Fatalf("open-session init trace must be display-only, got %+v", got)
	}

	aiHandler := newActivityTraceLineHandler(app, selection, sessionKindAI)
	aiHandler("==> Initializing erun/local")
	if got := app.activityQueue.list(); len(got) != 0 {
		t.Fatalf("ai-session init trace must be display-only, got %+v", got)
	}

	localHandler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	localHandler("==> Initializing erun/local")
	if _, ok := app.activityQueue.findActiveByCommand("init", "erun", "local"); !ok {
		t.Fatal("Local-session init trace must register an init activity")
	}
}

// A Windows ConPTY repaint re-sends "==> Initialized"; it must fire once per env.
func TestEmitEnvironmentInitializedFiresOncePerEnv(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	app.emitEnvironmentInitialized("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 1 {
		t.Fatalf("environment-initialized fired %d times for one env, want 1 (repaints must not re-fire)", got)
	}

	app.emitEnvironmentInitialized("frs", "other")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 2 {
		t.Fatalf("distinct env total = %d, want 2", got)
	}

	// Delete/failure resets, so a re-create fires again.
	app.clearInitEmitted("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	if got := len(emits.events(uimodel.EnvironmentInitializedEvent)); got != 3 {
		t.Fatalf("re-create after reset total = %d, want 3", got)
	}
}

func TestResolveActivityKubeContextFallsBackToEnvConfig(t *testing.T) {
	// Without the env-config fallback, kubectl would hit the host's current-context.
	app := newTestAppForActivityQueue(t)
	app.deps.store = stubUIStore{
		envs: map[string]config.EnvConfig{
			"erun/ux": {Name: "ux", KubernetesContext: "erun"},
		},
	}
	got := app.resolveActivityKubeContext(uimodel.Selection{}, "erun", "ux")
	if got != "erun" {
		t.Fatalf("kube context = %q, want %q", got, "erun")
	}
	got = app.resolveActivityKubeContext(uimodel.Selection{KubernetesContext: "session-context"}, "erun", "ux")
	if got != "session-context" {
		t.Fatalf("selection should win when present, got %q", got)
	}
}
