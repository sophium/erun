package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/podruntime"
)

// installedOrchestrateAgent is the path an installed erun-builder.md lands at
// under the test's confined $HOME. Mirrors installedOrchestrateSkill.
func installedOrchestrateAgent(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "agents", "erun-builder.md")
}

// clearAgentsOverride removes the exact-source override so a test exercises
// the resolution a desktop actually runs with. Mirrors clearSkillsOverride.
func clearAgentsOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_AGENTS_DIR", "")
}

// singleAgentSource writes a one-agent fixture source (erun-builder.md) and
// points ERUN_AGENTS_DIR at it, returning the source file path. Mirrors
// singleSkillSource.
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

// stampAgentsSource sets the build stamp for one test and restores it after.
// Mirrors stampSkillsSource.
func stampAgentsSource(t *testing.T, dir string) {
	t.Helper()
	previous := buildAgentsSource
	buildAgentsSource = dir
	t.Cleanup(func() { buildAgentsSource = previous })
}

// stampedAgentsCheckout stands in for the checkout a desktop build was
// produced from, in the layout that motivated the stamp: the running binary
// sits nowhere near a source tree, so the walk up from the executable
// resolves nothing and only the build stamp names the agents this build
// shipped. Returns the source file. Mirrors stampedSkillsCheckout.
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

	// Fixture agents source standing in for the repo's erun-skills/agents.
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

	// Idempotent + edit-preserving: a hand-edited installed agent survives a
	// re-run.
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

	// A newer agent ships; an untouched install must track it on next launch.
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

	// Operator edits the installed agent; then a newer source ships. The edit
	// must win over the refresh.
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

// TestOrchestratorAgentsRefreshFromBuildStampedCheckout covers the layout the
// desktop is actually built and run from: the bundle is copied out of its
// checkout, so nothing above the running binary names a source tree and only
// the build stamp resolves one. An installed copy erun itself wrote must
// track that source instead of freezing at whatever landed on first install.
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

	// The source changes and the desktop is restarted: the untouched install
	// must follow it.
	if err := os.WriteFile(srcMD, []byte("# v2 newer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if data, _ := os.ReadFile(installed); string(data) != "# v2 newer\n" {
		t.Fatalf("expected the untouched install refreshed from the stamped checkout, got %q", data)
	}
	// The marker moves with the refresh, so the next launch can still tell this
	// untouched copy from an edited one.
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

// TestOrchestratorAgentsPreserveEditFromBuildStampedCheckout keeps the
// operator's copy theirs in that same layout: resolving a source is not
// licence to overwrite an agent edited in place.
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

// TestOrchestratorAgentsReportUnresolvableSource locks the second half of the
// contract: when nothing resolves, the launch still succeeds and the operator
// is told once — a build that silently stops installing agents reads exactly
// like one where the agent had not changed.
func TestOrchestratorAgentsReportUnresolvableSource(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearAgentsOverride(t)
	exeDir := runFromBareLayout(t)
	// A checkout that is not on this machine, which is what a binary built
	// elsewhere carries.
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
	// The report has to be actionable on its own: what the build expected,
	// where the running binary looked instead, and both recoveries.
	for _, want := range []string{moved, exeDir, "ERUN_AGENTS_DIR", "build.sh"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("report does not name %q:\n%s", want, payload.Message)
		}
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "agents", "erun-builder.md")); !os.IsNotExist(err) {
		t.Fatalf("nothing resolvable must install nothing, stat err = %v", err)
	}
	// Once: the condition belongs to the build, so a second launch repeats it
	// in the log only.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if got := emits.events(appNotificationEvent); len(got) != 1 {
		t.Fatalf("expected the report said once, got %+v", got)
	}
}

// TestHostAgentsSourceOverrideWinsOverBuildStamp keeps ERUN_AGENTS_DIR meaning
// "use exactly this": it beats the build stamp, and an empty directory
// installs nothing rather than falling back to a source the caller did not
// name.
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

// TestOrchestratorAgentsResolveFromCheckoutAroundExecutable keeps the layout
// that already worked working: a binary sitting inside a checkout resolves
// that checkout's agents even with no build stamp, which is the case a
// packaged tree — or a binary built by anything other than these scripts —
// relies on.
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
	// Two levels down, the way a built binary sits under its checkout.
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
