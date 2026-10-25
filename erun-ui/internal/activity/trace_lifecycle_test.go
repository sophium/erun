package activity

import (
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

// Doctor runs piped into the Local shell with no PTY exit; trace lines are its only completion signal.
func TestActivityTraceLineHandlerStartsAndFinishesDoctor(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Doctor team/dev")
	entry, ok := app.queue.findActiveByCommand("doctor", "team", "dev")
	if !ok {
		t.Fatal("expected doctor entry to be active after ==> Doctor")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Doctor done team/dev")
	if _, ok := app.queue.findActiveByCommand("doctor", "team", "dev"); ok {
		t.Fatal("expected doctor entry to be finished after ==> Doctor done")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesDoctorOnFailure(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Doctor team/dev")
	handler("==> Doctor failed team/dev: kubectl not reachable")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if all[0].Error != "kubectl not reachable" {
		t.Fatalf("expected parsed reason, got %q", all[0].Error)
	}
}

// Locking would block doctor's interactive prompts.
func TestStartDoctorFromTraceDoesNotLockTerminal(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	localSession := Session{Selection: selection, Serial: 1, Kind: KindLocal}
	app.sessions.add(localSession)
	app.startDoctorFromTrace(selection, "team", "dev")
	if app.sessions.lockedBy(localSession.Serial) != "" {
		t.Fatalf("expected doctor to not lock terminal, got %q", app.sessions.lockedBy(localSession.Serial))
	}
}

func TestDoctorCompletedRecordsLastRunOutcome(t *testing.T) {
	type outcome struct {
		tenant, environment string
		success             bool
		message             string
	}
	var got []outcome
	app := newHarness(Deps{DoctorCompleted: func(tenant, environment string, success bool, message string) {
		got = append(got, outcome{tenant, environment, success, message})
	}})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Doctor team/dev")
	handler("==> Doctor failed team/dev: kubectl not reachable")

	want := []outcome{{"team", "dev", false, "kubectl not reachable"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("outcomes recorded = %+v, want %+v", got, want)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesSSHDInit(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> SSHD init team/dev")
	entry, ok := app.queue.findActiveByCommand("sshd-init", "team", "dev")
	if !ok {
		t.Fatal("expected sshd-init entry to be active after ==> SSHD init")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> SSHD init done team/dev")
	if _, ok := app.queue.findActiveByCommand("sshd-init", "team", "dev"); ok {
		t.Fatal("expected sshd-init entry to be finished after ==> SSHD init done")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesSSHDInitOnFailure(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> SSHD init team/dev")
	handler("==> SSHD init failed team/dev: sync remote authorized_keys from key.pub: exit status 1")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if all[0].Error != "sync remote authorized_keys from key.pub: exit status 1" {
		t.Fatalf("expected parsed reason, got %q", all[0].Error)
	}
}

func TestStartSSHDInitFromTraceLocksTerminals(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	localSession := Session{Selection: selection, Serial: 1, Kind: KindLocal}
	app.sessions.add(localSession)
	app.startSSHDInitFromTrace(selection, "team", "dev")
	if _, ok := app.queue.findActiveByCommand("sshd-init", "team", "dev"); !ok {
		t.Fatal("expected sshd-init entry to be active")
	}
}

func TestSSHDInitCompletedRecordsLastRunOutcome(t *testing.T) {
	type outcome struct {
		tenant, environment string
		success             bool
		message             string
	}
	var got []outcome
	app := newHarness(Deps{SSHDInitCompleted: func(tenant, environment string, success bool, message string) {
		got = append(got, outcome{tenant, environment, success, message})
	}})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> SSHD init team/dev")
	handler("==> SSHD init failed team/dev: kubectl not reachable")

	want := []outcome{{"team", "dev", false, "kubectl not reachable"}}
	if len(got) != 1 || got[0] != want[0] {
		t.Fatalf("outcomes recorded = %+v, want %+v", got, want)
	}
}

func TestStartCommandFromTraceDoesNotLockTerminal(t *testing.T) {
	// Locking would freeze the tab showing their output.
	for _, command := range []string{"build", "release", "push"} {
		app := newHarness(Deps{})
		selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
		envSession := Session{Selection: selection, Serial: 1, Kind: KindOpen}
		app.sessions.add(envSession)
		app.startCommandFromTrace(selection, command)
		if app.sessions.lockedBy(envSession.Serial) != "" {
			t.Fatalf("expected %s to not lock terminal, got %q", command, app.sessions.lockedBy(envSession.Serial))
		}
	}
}

// Only the Local session may own init: honoring a remote open's re-printed init
// lines would trigger a deploy→reopen loop.
func TestActivityTraceLineHandlerIgnoresInitFromNonLocalSession(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}

	openHandler := app.TraceLineHandler(selection, KindOpen)
	openHandler("==> Initializing erun/local")
	openHandler("==> Initialized erun/local")
	if _, ok := app.queue.findActiveByCommand("init", "erun", "local"); ok {
		t.Fatal("open-session init trace must not register an init activity")
	}
	if got := app.queue.list(); len(got) != 0 {
		t.Fatalf("open-session init trace must be display-only, got %+v", got)
	}

	aiHandler := app.TraceLineHandler(selection, KindAI)
	aiHandler("==> Initializing erun/local")
	if got := app.queue.list(); len(got) != 0 {
		t.Fatalf("ai-session init trace must be display-only, got %+v", got)
	}

	localHandler := app.TraceLineHandler(selection, KindLocal)
	localHandler("==> Initializing erun/local")
	if _, ok := app.queue.findActiveByCommand("init", "erun", "local"); !ok {
		t.Fatal("Local-session init trace must register an init activity")
	}
}

func TestResolveActivityKubeContextFallsBackToEnvConfig(t *testing.T) {
	// Without the env-config fallback, kubectl would hit the host's current-context.
	app := newHarness(Deps{Store: fakeStore{
		envs: map[string]config.EnvConfig{
			"erun/ux": {Name: "ux", KubernetesContext: "erun"},
		},
	}})
	got := app.resolveActivityKubeContext(uimodel.Selection{}, "erun", "ux")
	if got != "erun" {
		t.Fatalf("kube context = %q, want %q", got, "erun")
	}
	got = app.resolveActivityKubeContext(uimodel.Selection{KubernetesContext: "session-context"}, "erun", "ux")
	if got != "session-context" {
		t.Fatalf("selection should win when present, got %q", got)
	}
}

func envStatuses(emits *capturedEmits) []uimodel.EnvStatusPayload {
	var out []uimodel.EnvStatusPayload
	for _, raw := range emits.events(uimodel.EnvStatusEvent) {
		if payload, ok := raw.(uimodel.EnvStatusPayload); ok {
			out = append(out, payload)
		}
	}
	return out
}

// Tab presence alone is not running-ness; a successful deploy clears a stale failed flag.
func TestEnvStatusClearedByLaterSuccessfulDeploy(t *testing.T) {
	var deployed []string
	app := newHarness(Deps{EnvironmentDeployed: func(tenant, environment string) {
		deployed = append(deployed, tenant+"/"+environment)
	}})

	app.queue.start(Entry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uimodel.Selection{}, "erun", "remote", StatusSucceeded, "")

	statuses := envStatuses(app.emits)
	if len(statuses) != 1 || statuses[0] != (uimodel.EnvStatusPayload{Tenant: "erun", Environment: "remote"}) {
		t.Fatalf("expected a clearing env-status for the deployed env, got %+v", statuses)
	}
	if len(deployed) != 1 || deployed[0] != "erun/remote" {
		t.Fatalf("the create→deploy→open gate was released for %v, want erun/remote", deployed)
	}
}

func TestEnvStatusNotClearedByFailedDeploy(t *testing.T) {
	app := newHarness(Deps{EnvironmentDeployed: func(string, string) {
		t.Fatal("a failed deploy must not release the create→deploy→open gate")
	}})

	app.queue.start(Entry{Command: "deploy", Tenant: "erun", Environment: "remote", Version: "1.0.1"})
	app.finishDeployByTenantEnv(uimodel.Selection{}, "erun", "remote", StatusFailed, "==> Deploy failed after 2m1s")

	if got := envStatuses(app.emits); len(got) != 0 {
		t.Fatalf("a failed deploy must not emit env-status, got %+v", got)
	}
}
