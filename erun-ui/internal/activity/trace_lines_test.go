package activity

import (
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/primitive/open/appsession"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestActivityTraceLineHandlerFinalizesOnDeployedAndFailed(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}
	app.queue.start(Entry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler := app.TraceLineHandler(selection, KindLocal)
	handler("==> Deployed team/dev 1.0.0 in 12s")
	if _, ok := app.queue.findActive("team", "dev"); ok {
		t.Fatal("entry should be finished after ==> Deployed")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}

	app2 := newHarness(Deps{})
	app2.queue.start(Entry{Command: "deploy", Tenant: "team", Environment: "dev", Version: "1.0.0"})
	handler2 := app2.TraceLineHandler(selection, KindLocal)
	handler2("Error: UPGRADE FAILED: timeout")
	handler2("==> Deploy failed after 2m0s")
	all2 := app2.queue.list()
	if len(all2) != 1 || all2[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all2)
	}
	if !strings.Contains(all2[0].Error, "Deploy failed") && !strings.Contains(all2[0].Error, "UPGRADE FAILED") {
		t.Fatalf("error not captured: %q", all2[0].Error)
	}
}

func TestActivityTraceLineHandlerFinalizesOnReleaseNamedFailure(t *testing.T) {
	// Coupled to the CLI's failure-line wording.
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	app.queue.start(Entry{Command: "deploy", Tenant: "team", Environment: "dev"})
	handler := app.TraceLineHandler(selection, KindLocal)
	handler("Error: UPGRADE FAILED: timeout")
	handler("==> Deploy of team-devops failed after 2m0s")
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
}

func TestSessionReadyFailedMatchesReleaseNamedFailure(t *testing.T) {
	// The session-ready gate must match the same wording.
	if !sessionReadyFailedRe.MatchString("==> Deploy of team-devops failed after 2m0s") {
		t.Fatal("sessionReadyFailedRe must match the release-named failure line")
	}
	if !sessionReadyFailedRe.MatchString("==> Deploy failed after 2m0s") {
		t.Fatal("sessionReadyFailedRe must still match the no-release fallback")
	}
}

func TestActivityTraceLineHandlerLabelsComponentDeployByRelease(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)
	handler("==> Deploying erun/local · erun-backend-postgres 18.3")
	entry, ok := app.queue.findActiveByCommand("deploy", "erun", "local")
	if !ok {
		t.Fatal("expected a deploy entry after the component ==> Deploying line")
	}
	if entry.Release != "erun-backend-postgres" {
		t.Fatalf("expected release erun-backend-postgres, got %q", entry.Release)
	}
	if entry.Version != "18.3" {
		t.Fatalf("expected the component version 18.3, got %q", entry.Version)
	}
	if !strings.Contains(entry.Summary, "· erun-backend-postgres") {
		t.Fatalf("expected a component-labeled summary, got %q", entry.Summary)
	}
}

func TestActivityTraceLineHandlerRuntimeDeployFallsBackToRuntimeRelease(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)
	handler("==> Deploying erun/local 1.0.0")
	entry, ok := app.queue.findActiveByCommand("deploy", "erun", "local")
	if !ok {
		t.Fatal("expected a deploy entry after the runtime ==> Deploying line")
	}
	if entry.Release != releaseNameForTenant("erun") {
		t.Fatalf("expected runtime release %q, got %q", releaseNameForTenant("erun"), entry.Release)
	}
	if entry.Version != "1.0.0" {
		t.Fatalf("expected version 1.0.0, got %q", entry.Version)
	}
	if strings.Contains(entry.Summary, "·") {
		t.Fatalf("runtime deploy summary must not be component-labeled, got %q", entry.Summary)
	}
}

func TestActivityTraceLineHandlerFinalizesUsingTenantEnvFromLine(t *testing.T) {
	// A selection-less tab must still finalize via the tenant/env parsed from the line.
	app := newHarness(Deps{})
	app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "ux",
		Version:     "1.0.51-snapshot-20260508135009",
		Source:      "trace",
	})
	emptySelection := uimodel.Selection{}
	handler := app.TraceLineHandler(emptySelection, KindLocal)
	handler("==> Deployed erun/ux 1.0.51-snapshot-20260508135009 in 52s")
	if _, ok := app.queue.findActive("erun", "ux"); ok {
		t.Fatal("entry should be finished from line tenant/env even with empty selection")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesSkippingFromLine(t *testing.T) {
	// `==> Skipping` must also finalize from the parsed tenant/env.
	app := newHarness(Deps{})
	app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "erun",
		Environment: "ux",
		Source:      "trace",
	})
	handler := app.TraceLineHandler(uimodel.Selection{}, KindLocal)
	handler("==> Skipping erun/ux 1.0.51 (identical deploy already in progress)")
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSkipped {
		t.Fatalf("expected one skipped entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesBuild(t *testing.T) {
	// Build traces carry no tenant/env, so the handler keys off the selection.
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Building")
	entry, ok := app.queue.findActiveByCommand("build", "erun", "local")
	if !ok {
		t.Fatal("expected build entry to be active after ==> Building")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Built in 42s")
	if _, ok := app.queue.findActiveByCommand("build", "erun", "local"); ok {
		t.Fatal("expected build entry to be finished after ==> Built")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesBuildOnFailure(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Building")
	handler("==> Build failed after 5s")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Build failed") {
		t.Fatalf("expected Build failed in error, got %q", all[0].Error)
	}
}

// The handler must record Detail for subprocess callers with no PTY.
func TestActivityTraceLineHandlerRecordsDetailWithoutAPTY(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Building")
	handler("./main.go:12:2: undefined: fmt.Sprintfff")
	handler("==> Build failed after 3m12s")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Detail, "undefined: fmt.Sprintfff") {
		t.Fatalf("Detail missing the actual compiler error, got %q", all[0].Detail)
	}
	if strings.Contains(all[0].Error, "==>") || strings.Contains(all[0].Error, "after 3m12s") {
		t.Fatalf("Error should be cleaned of the trace marker and elapsed clause, got %q", all[0].Error)
	}
}

// A bare deploy-failure line must finalize the deploy, never a concurrent build.
func TestActivityTraceLineHandlerFinalizesCorrectCardWhenBuildAndDeployBothActive(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Building")
	if _, ok := app.queue.findActiveByCommand("build", "erun", "local"); !ok {
		t.Fatal("expected an active build entry")
	}
	app.queue.start(Entry{Command: "deploy", Tenant: "erun", Environment: "local"})

	handler("Error: UPGRADE FAILED: timeout")
	handler("==> Deploy failed after 4s")

	if _, ok := app.queue.findActiveByCommand("build", "erun", "local"); !ok {
		t.Fatal("the still-running build entry must not have been finalized by the deploy failure")
	}
	if _, ok := app.queue.findActiveByCommand("deploy", "erun", "local"); ok {
		t.Fatal("the deploy entry should have been finalized")
	}
	all := app.queue.list()
	var deployEntry *Entry
	for i := range all {
		if all[i].Command == "deploy" {
			deployEntry = &all[i]
		}
	}
	if deployEntry == nil || deployEntry.Status != StatusFailed {
		t.Fatalf("expected a failed deploy entry in history, got %+v", all)
	}
	if !strings.Contains(deployEntry.Error, "UPGRADE FAILED") && !strings.Contains(deployEntry.Detail, "UPGRADE FAILED") {
		t.Fatalf("expected the captured tool error on the deploy entry, got %+v", deployEntry)
	}
}

func TestActivityTraceLineHandlerSkipsBuildWithoutSelection(t *testing.T) {
	// An empty tenant/env entry would light up every sidebar row.
	app := newHarness(Deps{})
	handler := app.TraceLineHandler(uimodel.Selection{}, KindLocal)
	handler("==> Building")
	if len(app.queue.list()) != 0 {
		t.Fatalf("expected no entries registered without a selection, got %+v", app.queue.list())
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesRelease(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindOpen)

	handler("==> Releasing 1.4.2")
	entry, ok := app.queue.findActiveByCommand("release", "erun", "local")
	if !ok {
		t.Fatal("expected release entry to be active after ==> Releasing")
	}
	if entry.Source != "trace" {
		t.Fatalf("expected source=trace, got %q", entry.Source)
	}

	handler("==> Released 1.4.2 in 12s")
	if _, ok := app.queue.findActiveByCommand("release", "erun", "local"); ok {
		t.Fatal("expected release entry to be finished after ==> Released")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesReleaseOnFailure(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Releasing 1.4.2")
	handler("==> Release failed after 5s")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Release failed") {
		t.Fatalf("expected Release failed in error, got %q", all[0].Error)
	}
}

func TestActivityTraceLineHandlerStartsAndFinishesPush(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindOpen)

	handler("==> Pushing")
	if _, ok := app.queue.findActiveByCommand("push", "erun", "local"); !ok {
		t.Fatal("expected push entry to be active after ==> Pushing")
	}

	handler("==> Pushed in 8s")
	if _, ok := app.queue.findActiveByCommand("push", "erun", "local"); ok {
		t.Fatal("expected push entry to be finished after ==> Pushed")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusSucceeded {
		t.Fatalf("expected one succeeded entry, got %+v", all)
	}
}

func TestActivityTraceLineHandlerFinalizesPushOnFailure(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "erun", Environment: "local"}
	handler := app.TraceLineHandler(selection, KindLocal)

	handler("==> Pushing")
	handler("==> Push failed after 3s")

	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	if !strings.Contains(all[0].Error, "Push failed") {
		t.Fatalf("expected Push failed in error, got %q", all[0].Error)
	}
}

func TestActivityTraceLineHandlerRegistersForAllSessionKinds(t *testing.T) {
	// Every session kind registers from `==> Deploying`.
	cases := []string{KindLocal, KindCommand, KindOpen, KindAI}
	for _, kind := range cases {
		app := newHarness(Deps{})
		selection := uimodel.Selection{Tenant: "erun", Environment: "local", KubernetesContext: "orbstack"}
		handler := app.TraceLineHandler(selection, kind)
		handler("==> Deploying erun/local 1.0.51-snapshot-20260510080136")
		entry, ok := app.queue.findActiveByCommand("deploy", "erun", "local")
		if !ok {
			t.Fatalf("kind %q: expected deploy auto-registered from trace", kind)
		}
		if entry.Version != "1.0.51-snapshot-20260510080136" {
			t.Fatalf("kind %q: version = %q", kind, entry.Version)
		}
		if entry.Source != "trace" {
			t.Fatalf("kind %q: source = %q, want trace", kind, entry.Source)
		}
	}
}

// Output must be buffered before the failure line finalizes the entry, across
// chunks that split lines anywhere.
func TestFeedTraceCapturesFailureDetail(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev", Version: "1.0.0"}

	// Seeded directly: the trace start path needs env config this test does not stage.
	app.queue.start(Entry{
		Command:     "deploy",
		Tenant:      "team",
		Environment: "dev",
		Version:     "1.0.0",
	})

	buffer := ""
	var readyErr error
	feed := func(chunk string) {
		var lines []string
		buffer, lines = DrainTraceLines(buffer + StripTraceANSI(chunk))
		app.FeedTrace(selection, KindLocal, lines, func(err error) { readyErr = err })
	}
	feed("helm upgrade --install team-devops ./chart\r\nError: UPGRADE ")
	feed("FAILED: timed out waiting for the condition\r\n")
	feed("\x1b[31m==> Deploy failed after 4s\x1b[0m\r\n")

	if _, ok := app.queue.findActive("team", "dev"); ok {
		t.Fatal("entry should be finalized out of active after the failure line")
	}
	all := app.queue.list()
	if len(all) != 1 || all[0].Status != StatusFailed {
		t.Fatalf("expected one failed entry, got %+v", all)
	}
	for _, want := range []string{"helm upgrade --install", "UPGRADE FAILED", "==> Deploy failed after 4s"} {
		if !strings.Contains(all[0].Detail, want) {
			t.Fatalf("Detail missing %q, got %q", want, all[0].Detail)
		}
	}
	if readyErr == nil {
		t.Fatal("the failure line must release the session's ready gate with its error")
	}
}

func TestFeedTraceReportsATakenOverSession(t *testing.T) {
	app := newHarness(Deps{})
	selection := uimodel.Selection{Tenant: "team", Environment: "dev"}
	if app.FeedTrace(selection, KindOpen, []string{"plain output"}, nil) {
		t.Fatal("ordinary output must not read as a takeover")
	}
	if !app.FeedTrace(selection, KindOpen, []string{appsession.ShellSessionTakenOverNotice}, nil) {
		t.Fatal("the takeover notice must be reported")
	}
}

func TestDrainTraceLinesKeepsThePartialLineAndTheLastOverwrite(t *testing.T) {
	rest, lines := DrainTraceLines("spinner 1\rspinner 2\r\ndone\npartial")
	if rest != "partial" {
		t.Fatalf("rest = %q, want the trailing partial line", rest)
	}
	if len(lines) != 2 || lines[0] != "spinner 2" || lines[1] != "done" {
		t.Fatalf("lines = %q, want the text after the last overwrite", lines)
	}
}
