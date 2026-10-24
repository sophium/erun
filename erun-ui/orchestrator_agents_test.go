package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/podruntime"
)

func installedOrchestrateAgent(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "agents", "erun-builder.md")
}

func clearAgentsOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_AGENTS_DIR", "")
}

func singleAgentSource(t *testing.T, body string) string {
	t.Helper()
	srcRoot := t.TempDir()
	srcMD := filepath.Join(srcRoot, "erun-builder.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ERUN_AGENTS_DIR", srcRoot)
	return srcMD
}

func stampAgentsSource(t *testing.T, dir string) {
	t.Helper()
	previous := buildAgentsSource
	buildAgentsSource = dir
	t.Cleanup(func() { buildAgentsSource = previous })
}

// stampedAgentsCheckout stands in for a build-stamped checkout the binary is not
// inside, so only the stamp resolves the agents source.
func stampedAgentsCheckout(t *testing.T, body string) string {
	t.Helper()
	clearAgentsOverride(t)
	runFromBareLayout(t)
	agentsRoot := filepath.Join(t.TempDir(), "checkout", "erun-skills", "agents")
	if err := os.MkdirAll(agentsRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	srcMD := filepath.Join(agentsRoot, "erun-builder.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stampAgentsSource(t, agentsRoot)
	return srcMD
}

func TestOrchestratorAgentsInstalledByDefault(t *testing.T) {
	app := orchestratorTestApp(t) // confines $HOME to a temp dir
	defer app.shutdown(context.Background())

	srcRoot := t.TempDir()
	for _, name := range []string{"erun-builder", "erun-reviewer"} {
		if err := os.WriteFile(filepath.Join(srcRoot, name+".md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ERUN_AGENTS_DIR", srcRoot)

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	agentsRoot := filepath.Join(home, ".claude", "agents")
	for _, name := range []string{"erun-builder", "erun-reviewer"} {
		if _, err := os.Stat(filepath.Join(agentsRoot, name+".md")); err != nil {
			t.Fatalf("agent %q not installed by default: %v", name, err)
		}
	}

	edited := filepath.Join(agentsRoot, "erun-builder.md")
	if err := os.WriteFile(edited, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace failed: %v", err)
	}
	if data, _ := os.ReadFile(edited); string(data) != "EDITED" {
		t.Fatalf("expected in-place agent edit preserved, got %q", data)
	}
}

func TestOrchestratorAgentsRefreshWhenSourceChanges(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleAgentSource(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateAgent(t)
	if data, _ := os.ReadFile(installed); string(data) != "# v1\n" {
		t.Fatalf("expected installed v1, got %q", data)
	}

	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# v2 newer\n" {
		t.Fatalf("expected untouched install refreshed to v2, got %q", data)
	}
}

func TestOrchestratorAgentsPreserveEditAcrossSourceChange(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleAgentSource(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateAgent(t)

	if err := os.WriteFile(installed, []byte("# operator edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# operator edit\n" {
		t.Fatalf("expected operator edit preserved across a source change, got %q", data)
	}
}

func TestOrchestratorAgentsRefreshFromBuildStampedCheckout(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())
	srcMD := stampedAgentsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateAgent(t)
	if data, _ := os.ReadFile(installed); string(data) != "# v1\n" {
		t.Fatalf("expected the stamped checkout's agent installed, got %q", data)
	}

	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# v2 newer\n" {
		t.Fatalf("expected the untouched install refreshed from the stamped checkout, got %q", data)
	}
	marker, err := os.ReadFile(installed + podruntime.AgentFileMarkerSuffix)
	if err != nil {
		t.Fatalf("read agent marker: %v", err)
	}
	want, err := podruntime.FileSHA256(srcMD)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(marker)) != want {
		t.Fatalf("marker = %q, want the refreshed source sha %q", strings.TrimSpace(string(marker)), want)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}

func TestOrchestratorAgentsPreserveEditFromBuildStampedCheckout(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := stampedAgentsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateAgent(t)
	if err := os.WriteFile(installed, []byte("# operator edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# operator edit\n" {
		t.Fatalf("expected the operator's edit preserved, got %q", data)
	}
}

func TestOrchestratorAgentsReportUnresolvableSource(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearAgentsOverride(t)
	exeDir := runFromBareLayout(t)
	// A checkout that is not on this machine, as a binary built elsewhere carries.
	moved := filepath.Join(t.TempDir(), "checkout-that-moved", "erun-skills", "agents")
	stampAgentsSource(t, moved)

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("the orchestrator must still launch cleanly: %v", err)
	}
	notes := emits.events(appNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("expected exactly one report, got %+v", notes)
	}
	payload, ok := notes[0].(appNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", notes[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	for _, want := range []string{moved, exeDir, "ERUN_AGENTS_DIR", "build.sh"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("report does not name %q:\n%s", want, payload.Message)
		}
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "agents", "erun-builder.md")); !os.IsNotExist(err) {
		t.Fatalf("nothing resolvable must install nothing, stat err = %v", err)
	}
	// Once: a second launch repeats it in the log only.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if got := emits.events(appNotificationEvent); len(got) != 1 {
		t.Fatalf("expected the report said once, got %+v", got)
	}
}

func TestHostAgentsSourceOverrideWinsOverBuildStamp(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	stampedAgentsCheckout(t, "# stamped\n")
	override := t.TempDir()
	t.Setenv("ERUN_AGENTS_DIR", override)

	resolved, err := hostAgentsSource()
	if err != nil {
		t.Fatalf("an override must resolve: %v", err)
	}
	if resolved != override {
		t.Fatalf("source = %q, want the override %q", resolved, override)
	}

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "agents", "erun-builder.md")); !os.IsNotExist(err) {
		t.Fatalf("an empty override must install nothing, stat err = %v", err)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("an honoured override is not a failure to report, got %+v", notes)
	}
}

func TestOrchestratorAgentsResolveFromCheckoutAroundExecutable(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearAgentsOverride(t)
	stampAgentsSource(t, "")

	checkout := t.TempDir()
	agentsDir := filepath.Join(checkout, "erun-skills", "agents")
	if err := os.MkdirAll(agentsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agentsDir, "erun-builder.md"), []byte("# from the checkout\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	exeDir := filepath.Join(checkout, "erun-ui", "bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	previous := runningExecutable
	runningExecutable = func() (string, error) { return filepath.Join(exeDir, "erun-app"), nil }
	t.Cleanup(func() { runningExecutable = previous })

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installedOrchestrateAgent(t)); string(data) != "# from the checkout\n" {
		t.Fatalf("expected the surrounding checkout's agent installed, got %q", data)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}
