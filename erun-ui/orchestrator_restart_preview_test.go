package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

// Wire shapes are declared here, not borrowed from the Go types, so the test reads
// what an external trigger reads.
type restartDryRunReopen struct {
	OrchestratorID string `json:"orchestratorId"`
	Notice         string `json:"notice"`
}

type restartDryRunResponse struct {
	OK      bool                  `json:"ok"`
	Error   string                `json:"error,omitempty"`
	Preview []restartDryRunReopen `json:"preview,omitempty"`
}

func postRestartDryRun(t *testing.T, port int, orchestratorID string) restartDryRunResponse {
	t.Helper()
	body, err := json.Marshal(map[string]any{"orchestratorId": orchestratorID})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	resp, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d%s", port, core.DesktopControlPlanPath), "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST restart plan control: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Check status first: reading another page as a plan turns a question into a command.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the plan request was not served: HTTP %d — nothing in the running desktop answers what a restart would reopen", resp.StatusCode)
	}
	var decoded restartDryRunResponse
	if err := json.NewDecoder(resp.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return decoded
}

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

func assertPlanNames(t *testing.T, what, text string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(text, want) {
			t.Fatalf("expected %s to name %q, got %q", what, want, text)
		}
	}
}

func stagedRestoreIDs(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read restore dir: %v", err)
	}
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, strings.TrimSuffix(entry.Name(), ".json"))
	}
	sort.Strings(out)
	return out
}

func assertNothingRestarted(t *testing.T, relaunches int, restoreDir string) {
	t.Helper()
	if relaunches != 0 {
		t.Fatalf("a dry run relaunched the desktop %d times", relaunches)
	}
	if staged := stagedRestoreIDs(t, restoreDir); len(staged) != 0 {
		t.Fatalf("a dry run staged a hand-off for %v", staged)
	}
}

// stagedRestartFixture leaves one orchestrator running that the next desktop would refuse to reopen.
func stagedRestartFixture(t *testing.T, app *App) (asking, refused string) {
	t.Helper()
	asking = createAndStartNamedOrchestrator(t, app, "erun", "dev")
	refused = createAndStartNamedOrchestrator(t, app, "petios-ops", "laptop")
	editStoredOrchestrator(app, refused, func(o *config.OrchestratorConfig) { o.UseGateway = true })
	return asking, refused
}

// The plan names only refused orchestrators; warning about the rest would teach
// the operator to ignore it.
func TestRestartDryRunNamesTheOrchestratorsItWouldNotReopen(t *testing.T) {
	app, restoreDir := restartTestApp(t)
	relaunches := 0
	app.deps.relaunchApp = func() error { relaunches++; return nil }

	asking, refused := stagedRestartFixture(t, app)

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
	assertPlanNames(t, "the warning", restartPlanRow(t, plan.Preview, refused).Notice,
		"will not reopen "+refused, "no gateway catalog is configured")
	if row := restartPlanRow(t, plan.Preview, asking); row.Notice != "" {
		t.Fatalf("expected %q to reopen with nothing to say, got %+v", asking, row)
	}

	assertNothingRestarted(t, relaunches, restoreDir)
}

// The plan and the relaunch are computed in different processes, so hold them to agree.
func TestTheRestartPlanAgreesWithWhatTheNextLaunchReopens(t *testing.T) {
	app, _ := restartTestApp(t)
	app.deps.relaunchApp = func() error { return nil }

	asking, refused := stagedRestartFixture(t, app)

	server, port := startRestartControlServer(app)
	if server == nil {
		t.Fatal("expected the control server to bind a loopback listener")
	}
	plan := postRestartDryRun(t, port, asking)
	server.Close()
	planned := restartPlanRow(t, plan.Preview, refused)

	if err := app.RestartApp(asking); err != nil {
		t.Fatalf("RestartApp failed: %v", err)
	}
	target := app.ResolveOrchestratorToReopen()
	if target.OrchestratorID != asking || len(target.AlsoReopen) != 1 || target.AlsoReopen[0].OrchestratorID != refused {
		t.Fatalf("expected %q to own the pane with %q reopened beside it, got %+v", asking, refused, target)
	}
	app.stopOrchestratorSession(refused)
	_, err := app.StartOrchestrator(refused, 80, 24)
	if err == nil {
		t.Fatalf("the plan said %q would not reopen, but it did", refused)
	}
	assertPlanNames(t, "the plan", planned.Notice, "no gateway catalog is configured")
	assertPlanNames(t, "the refusal", err.Error(), "no gateway catalog is configured")
}
