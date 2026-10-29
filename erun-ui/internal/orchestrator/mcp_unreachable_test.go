package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestOrchestratorMCPUnreachableEnv(t *testing.T) {
	tenant, environment, ok := orchestratorMCPUnreachableEnv("frs/dev")
	if !ok || tenant != "frs" || environment != "dev" {
		t.Fatalf("got tenant=%q environment=%q ok=%v, want frs/dev/true", tenant, environment, ok)
	}
	for _, label := range []string{"", "frs", "/dev", "frs/", "?/dev", "frs/?", "an unnamed linked entry"} {
		if tenant, environment, ok := orchestratorMCPUnreachableEnv(label); ok {
			t.Fatalf("label %q resolved to %q/%q, want no actionable env", label, tenant, environment)
		}
	}
}

func TestOrchestratorMCPUnreachableNoticeNamesTheEnvironments(t *testing.T) {
	if got := orchestratorMCPUnreachableNotice("Petios", nil); got != "" {
		t.Fatalf("expected no notice when nothing is unreachable, got %q", got)
	}
	notice := orchestratorMCPUnreachableNotice("Petios", []orchestratorMCPUnreachable{{Label: "frs/dev"}})
	for _, want := range []string{"Petios", "frs/dev", "not answering"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("notice does not mention %q: %q", want, notice)
		}
	}
}

func TestWireOrchestratorMCPWiresAnUnreachableEnvAndSaysSo(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app, _ := orchestratorTestAppWithReachability(t, func(int) bool { return false })
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	path := app.wireOrchestratorMCP("petios", "Petios", []configpkg.OrchestratorEnvConfig{{Tenant: "frs", Environment: "dev"}})
	if strings.TrimSpace(path) == "" {
		t.Fatal("expected an MCP config path even though the edge is unreachable")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read written config: %v", err)
	}
	if !strings.Contains(string(data), "frs-dev") {
		t.Fatalf("expected the unreachable env still wired into the config:\n%s", data)
	}

	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 1 {
		t.Fatalf("expected exactly one notice about the unreachable edge, got %+v", events)
	}
	payload, ok := events[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", events[0])
	}
	if !strings.Contains(payload.Message, "frs/dev") || !strings.Contains(payload.Message, "not answering") {
		t.Fatalf("notice does not name the environment and the reason: %q", payload.Message)
	}
	// A single unreachable env makes the notice unambiguous, so it carries the deploy action.
	wantTag := [4]string{"frs", "dev", notificationSourceOrchestratorEdgeUnreachable, uimodel.NotificationActionDeploy}
	gotTag := [4]string{payload.Tenant, payload.Environment, payload.Source, payload.Action}
	if gotTag != wantTag {
		t.Fatalf("notice tenant/environment/source/action = %+v, want %+v", gotTag, wantTag)
	}
}

func TestWireOrchestratorMCPMultipleUnreachableEnvsAreEachActionable(t *testing.T) {
	t.Setenv("ERUN_ERUN_BIN", filepath.Join(t.TempDir(), "erun"))
	app, _ := orchestratorTestAppWithReachability(t, func(int) bool { return false })
	defer app.shutdown(context.Background())
	emits := newCapturedEmits()
	app.emitFn = emits.fn()

	app.wireOrchestratorMCP("petios", "Petios", []configpkg.OrchestratorEnvConfig{
		{Tenant: "frs", Environment: "dev"},
		{Tenant: "frs", Environment: "laptop"},
	})

	events := emits.events(uimodel.AppNotificationEvent)
	if len(events) != 2 {
		t.Fatalf("expected one notice per unreachable env, got %+v", events)
	}
	seen := make(map[string]bool, len(events))
	for _, event := range events {
		seen[assertActionableUnreachableNotice(t, event, "Petios")] = true
	}
	for _, want := range []string{"frs/dev", "frs/laptop"} {
		if !seen[want] {
			t.Fatalf("no actionable notice for %s; got %+v", want, seen)
		}
	}
}

// assertActionableUnreachableNotice returns the env the notice names and targets with a deploy action.
func assertActionableUnreachableNotice(t *testing.T, event any, orchestrator string) string {
	t.Helper()
	payload, ok := event.(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", event)
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning: %q", payload.Kind, payload.Message)
	}
	if payload.Action != uimodel.NotificationActionDeploy {
		t.Fatalf("notice for %s/%s carries action %q, want %q -- every unreachable edge must be actionable",
			payload.Tenant, payload.Environment, payload.Action, uimodel.NotificationActionDeploy)
	}
	if payload.Source != notificationSourceOrchestratorEdgeUnreachable {
		t.Fatalf("notice source = %q, want %q", payload.Source, notificationSourceOrchestratorEdgeUnreachable)
	}
	label := payload.Tenant + "/" + payload.Environment
	for _, want := range []string{label, orchestrator} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("notice tagged %s does not mention %q: %q", label, want, payload.Message)
		}
	}
	return label
}
