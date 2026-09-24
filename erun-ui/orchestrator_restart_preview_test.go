package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	eruncommon "github.com/sophium/erun/erun-common"
)

// The dry-run wire shapes, declared here rather than borrowed from the Go types
// behind them on purpose: an external trigger is what asks this question, so
// what the test reads is what such a trigger reads. A shape that only exists in
// the same package would pass whatever the package happens to produce.
type restartDryRunReopen struct {
	OrchestratorID string `json:"orchestratorId"`
	ConversationID string `json:"conversationId"`
	Notice         string `json:"notice"`
}

type restartDryRunResponse struct {
	OK      bool                  `json:"ok"`
	Error   string                `json:"error,omitempty"`
	Preview []restartDryRunReopen `json:"preview,omitempty"`
}

// postRestartDryRun asks the running desktop what a restart would reopen
// WITHOUT restarting anything, exactly as a dry-run trigger does.
func postRestartDryRun(t *testing.T, port int, orchestratorID string) restartDryRunResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"orchestratorId": orchestratorID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, eruncommon.DesktopControlPlanPath), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST restart plan control: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// The status is the whole safety of this request, so it is asserted before
	// the body is read: a desktop that does not serve the plan path answers with
	// a page of its own rather than a plan, and reading that as a plan is how a
	// question becomes a command.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the plan request was not served: HTTP %d — nothing in the running desktop answers what a restart would reopen", resp.StatusCode)
	}
	var decoded restartDryRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

// restartPlanRow finds one orchestrator's row in a plan, failing when the plan
// never named it: an orchestrator missing from the plan is the defect, not a
// lookup that found nothing.
func restartPlanRow(t *testing.T, preview []restartDryRunReopen, id string) restartDryRunReopen {
	t.Helper()
	for _, row := range preview {
		if row.OrchestratorID == id {
			return row
		}
	}
	t.Fatalf("expected the plan to name %q, got %+v", id, preview)
	return restartDryRunReopen{}
}

// assertPlanNames fails unless every fragment appears in the reported text.
func assertPlanNames(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %s to name %q, got %q", what, want, text)
		}
	}
}

// assertNothingRestarted fails when a dry run did anything but ask: no relaunch,
// no resume hand-off staged.
func assertNothingRestarted(t *testing.T, relaunches int, restoreDir string) {
	t.Helper()
	if relaunches != 0 {
		t.Fatalf("a dry run relaunched the desktop %d times", relaunches)
	}
	if staged := stagedRestoreIDs(t, restoreDir); len(staged) != 0 {
		t.Fatalf("a dry run staged a resume hand-off for %v", staged)
	}
}

// reopenedConversationFor reads the conversation one orchestrator is reopened on
// by the launch target, whether it owns the pane or comes back alongside it.
func reopenedConversationFor(target relaunchTarget, id string) string {
	if target.OrchestratorID == id {
		return target.ConversationID
	}
	for _, ref := range target.AlsoReopen {
		if ref.OrchestratorID == id {
			return ref.ConversationID
		}
	}
	return ""
}

// stagedRestartFixture stages the state the reported incident describes: two
// orchestrators open, neither with a conversation attached, and each one's own
// session working in a conversation that is NOT the anchor derived from its id.
// It returns each one's derived anchor and the conversation its session was
// really working in.
//
// The one that asks for the restart (asking) has the same diverging record as
// the other, and that is the point of keeping it in the fixture: the restart
// hands it back the conversation its own session is on, so it must come out of
// the preview unresumed-by-anchor, while `other` — which no hand-off reaches —
// is the one the operator can still do something about.
func stagedRestartFixture(t *testing.T, app *App) (asking, other, askingLive, otherLive string) {
	t.Helper()
	asking = createAndStartNamedOrchestrator(t, app, "erun", "dev")
	other = createAndStartNamedOrchestrator(t, app, "petios-ops", "laptop")
	askingLive = "3bde477f-6a1c-4a2e-9f0d-2c5b8e7a1d34"
	otherLive = "6942fcd4-2f8b-4c6d-8e10-7a3d9b5c4e21"
	for _, staged := range []struct {
		id, live string
	}{
		{asking, askingLive},
		{other, otherLive},
	} {
		anchor := orchestratorSessionID(staged.id)
		if anchor == "" || anchor == staged.live {
			t.Fatalf("fixture needs a conversation distinct from the anchor of %s", staged.id)
		}
		stageOrchestratorConversation(t, anchor)
		stageOrchestratorConversation(t, staged.live)
		writeLiveConversationRecord(t, staged.id, orchestratorLiveConversation{
			ConversationID: staged.live,
			LaunchID:       recordedLaunchID(t, app.deps.orchestratorOpenPath, staged.id),
		})
	}
	return asking, other, askingLive, otherLive
}

// The reported flow at the boundary the operator actually uses, and the whole
// defect in one assertion: the ONLY moment an operator can still do something
// about a stranding restart is before it happens, and before it happens nothing
// said anything. This asks the running desktop what a restart triggered now
// would reopen, and requires the answer to name the orchestrator that will not
// come back to the conversation its session was working in — together with the
// conversation it lands on instead and the one it leaves behind.
func TestRestartDryRunNamesTheOrchestratorsItWouldNotResume(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	relaunches := 0
	app.deps.relaunchApp = func() error { relaunches++; return nil }

	asking, other, askingLive, otherLive := stagedRestartFixture(t, app)

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	defer server.Close()

	plan := postRestartDryRun(t, port, asking)
	if !plan.OK {
		t.Fatalf("expected ok=true from the dry run, got %+v", plan)
	}
	if len(plan.Preview) != 2 {
		t.Fatalf("expected both open orchestrators in the plan, got %+v", plan.Preview)
	}

	stranded := restartPlanRow(t, plan.Preview, other)
	if stranded.ConversationID != orchestratorSessionID(other) {
		t.Fatalf("expected %q to be reported landing on its anchor %q, got %q",
			other, orchestratorSessionID(other), stranded.ConversationID)
	}
	assertPlanNames(t, "the warning", stranded.Notice, otherLive, orchestratorSessionID(other), "manage the orchestrator")

	// The one that asked for the restart is not stranded and must not be
	// reported as if it were: the restart hands it back the conversation its own
	// session is on. A plan that warned about it would teach the operator to
	// ignore the plan.
	if resumed := restartPlanRow(t, plan.Preview, asking); resumed.ConversationID != askingLive || resumed.Notice != "" {
		t.Fatalf("expected %q to be reported coming back to its live conversation %q with nothing to say, got %+v",
			asking, askingLive, resumed)
	}

	// Asking what would happen is not the same as doing it.
	assertNothingRestarted(t, relaunches, restoreDir)
}

// A warning is worth nothing unless it is the warning the restart then produces
// — and the two are computed on opposite sides of a process boundary, from
// state the restart itself is about to change. So the plan is held to what the
// next launch actually does: the same orchestrator, the same conversation left
// behind, the same one landed on.
func TestTheRestartPlanAgreesWithWhatTheNextLaunchResumes(t *testing.T) {
	app, _ := restartTestApp(t)
	app.deps.relaunchApp = func() error { return nil }

	asking, other, _, otherLive := stagedRestartFixture(t, app)

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	plan := postRestartDryRun(t, port, asking)
	server.Close()

	planned := restartPlanRow(t, plan.Preview, other)
	if planned.Notice == "" {
		t.Fatalf("expected the plan to warn about %q, got %+v", other, plan.Preview)
	}

	if err := app.RestartApp(asking); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	target := app.ResolveOrchestratorToReopen()

	if landed := reopenedConversationFor(target, other); landed != planned.ConversationID {
		t.Fatalf("the plan said %q would reopen on %q, the launch put it on %q", other, planned.ConversationID, landed)
	}
	assertPlanNames(t, "the launch's notices", noticeText(target.Notices), planned.Notice, otherLive)
}
