package main

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
)

// TestActivityTraceLineHandlerStartsAndFinishesDoctor: `erun doctor` runs
// piped into the shared Local shell, which never produces a PTY exit, so the
// `==> Doctor ...` trace lines are its only completion signal — mirroring
// init/build, not a bespoke mechanism.
func TestActivityTraceLineHandlerStartsAndFinishesDoctor(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev"}
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
	selection := uiSelection{Tenant: "team", Environment: "dev"}
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

// TestStartDoctorFromTraceDoesNotLockTerminal: doctor's recovery actions
// prompt interactively in the very terminal it runs in, so unlike deploy/init
// it must not lock terminals — locking would block the prompt it is waiting on.
func TestStartDoctorFromTraceDoesNotLockTerminal(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev"}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 1, kind: sessionKindLocal}
	app.sessions[localSession.key] = localSession
	app.startDoctorFromTrace(selection, "team", "dev")
	if localSession.lockedByActivity != "" {
		t.Fatalf("expected doctor to not lock terminal, got %q", localSession.lockedByActivity)
	}
}

// TestDoctorCompletedEventRecordsLastRunOutcome: doctor's outcome must reach
// the frontend even though it runs piped into the shared Local shell (no PTY
// exit) — this is what the Manage dialog's SSH tab renders as the persisted
// last-run result.
func TestDoctorCompletedEventRecordsLastRunOutcome(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uiSelection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> Doctor team/dev")
	handler("==> Doctor failed team/dev: kubectl not reachable")

	events := emits.events(doctorCompletedEvent)
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

// TestActivityTraceLineHandlerStartsAndFinishesSSHDInit: `erun sshd init` runs
// piped into the shared Local shell, which never produces a PTY exit, so the
// `==> SSHD init ...` trace lines are its only completion signal — the same
// structural defect erun#1268 fixed for doctor.
func TestActivityTraceLineHandlerStartsAndFinishesSSHDInit(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev"}
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
	selection := uiSelection{Tenant: "team", Environment: "dev"}
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

// TestStartSSHDInitFromTraceLocksTerminals: sshd init runs no interactive
// recovery prompts of its own — unlike doctor, it is a one-shot provisioning
// action like init/deploy — so it takes the same lockTerminalsForActivity
// path those do (today a no-op for non-deploy commands per
// sessionMatchesActivity, but consistent with init's own call).
func TestStartSSHDInitFromTraceLocksTerminals(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "team", Environment: "dev"}
	localSession := &managedTerminal{selection: selection, key: "local\x00team\x00dev", serial: 1, kind: sessionKindLocal}
	app.sessions[localSession.key] = localSession
	app.startSSHDInitFromTrace(selection, "team", "dev")
	if _, ok := app.activityQueue.findActiveByCommand("sshd-init", "team", "dev"); !ok {
		t.Fatal("expected sshd-init entry to be active")
	}
}

// TestSSHDInitCompletedEventRecordsLastRunOutcome: sshd init's outcome must
// reach the frontend even though it runs piped into the shared Local shell (no
// PTY exit) — this is what the Manage dialog's SSH access section renders as
// the persisted last-run result.
func TestSSHDInitCompletedEventRecordsLastRunOutcome(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	selection := uiSelection{Tenant: "team", Environment: "dev"}
	handler := newActivityTraceLineHandler(app, selection, sessionKindLocal)

	handler("==> SSHD init team/dev")
	handler("==> SSHD init failed team/dev: kubectl not reachable")

	events := emits.events(sshdInitCompletedEvent)
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
	// Build/release/push run IN the user's terminal, so locking the session
	// would freeze the very tab they are reading output in — unlike deploy, they
	// must not take the concurrency lock.
	for _, command := range []string{"build", "release", "push"} {
		app := newTestAppForActivityQueue(t)
		selection := uiSelection{Tenant: "erun", Environment: "local"}
		envSession := &managedTerminal{selection: selection, key: "env\x00erun\x00local", serial: 1, kind: sessionKindOpen}
		app.sessions[envSession.key] = envSession
		app.startCommandFromTrace(selection, command)
		if envSession.lockedByActivity != "" {
			t.Fatalf("expected %s to not lock terminal, got %q", command, envSession.lockedByActivity)
		}
	}
}

// TestActivityTraceLineHandlerIgnoresInitFromNonLocalSession guards the fix for
// the deploy⇄reopen loop: a remote-agent `erun open` re-runs the remote-worktree
// bootstrap and prints the init lifecycle ("==> Initializing" / "==> Initialized"),
// but only the Local session that runs `erun init` may own it. Honoring it from an
// open/ai session made the desktop fire a spurious deploy, which rolled the pod,
// killed the open shell, respawned it, and re-emitted the line — an endless loop.
func TestActivityTraceLineHandlerIgnoresInitFromNonLocalSession(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	selection := uiSelection{Tenant: "erun", Environment: "local"}

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

	// The Local session that actually runs `erun init` still owns the lifecycle.
	localHandler := newActivityTraceLineHandler(app, selection, sessionKindLocal)
	localHandler("==> Initializing erun/local")
	if _, ok := app.activityQueue.findActiveByCommand("init", "erun", "local"); !ok {
		t.Fatal("Local-session init trace must register an init activity")
	}
}

// TestEmitEnvironmentInitializedFiresOncePerEnv guards the create→deploy loop
// fix: a Windows ConPTY repaint re-sends the "==> Initialized" trace as fresh
// output, so the event must fire at most once per env (each re-fire composed
// another deploy, an endless loop), and reset for a re-create.
func TestEmitEnvironmentInitializedFiresOncePerEnv(t *testing.T) {
	app := newTestAppForActivityQueue(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())

	app.emitEnvironmentInitialized("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	if got := len(emits.events(environmentInitializedEvent)); got != 1 {
		t.Fatalf("environment-initialized fired %d times for one env, want 1 (repaints must not re-fire)", got)
	}

	// A different env is independent.
	app.emitEnvironmentInitialized("frs", "other")
	if got := len(emits.events(environmentInitializedEvent)); got != 2 {
		t.Fatalf("distinct env total = %d, want 2", got)
	}

	// Delete/failure resets, so a legitimate re-create fires again.
	app.clearInitEmitted("frs", "dev")
	app.emitEnvironmentInitialized("frs", "dev")
	if got := len(emits.events(environmentInitializedEvent)); got != 3 {
		t.Fatalf("re-create after reset total = %d, want 3", got)
	}
}

func TestResolveActivityKubeContextFallsBackToEnvConfig(t *testing.T) {
	// Without the env-config fallback, a selection-less Local tab emits an empty
	// KubernetesContext and the container-status poller's kubectl hits the host's
	// `current-context` (orbstack on a dev machine), not the env's cluster — so
	// the deploy card renders with no container pills.
	app := newTestAppForActivityQueue(t)
	app.deps.store = stubUIStore{
		envs: map[string]config.EnvConfig{
			"erun/ux": {Name: "ux", KubernetesContext: "erun"},
		},
	}
	got := app.resolveActivityKubeContext(uiSelection{}, "erun", "ux")
	if got != "erun" {
		t.Fatalf("kube context = %q, want %q", got, "erun")
	}
	got = app.resolveActivityKubeContext(uiSelection{KubernetesContext: "session-context"}, "erun", "ux")
	if got != "session-context" {
		t.Fatalf("selection should win when present, got %q", got)
	}
}
