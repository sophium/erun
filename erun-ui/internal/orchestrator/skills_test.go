package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/podruntime"
	"github.com/sophium/erun/erun-ui/internal/uimodel"
)

func TestOrchestratorSkillsInstalledByDefault(t *testing.T) {
	app := orchestratorTestApp(t) // confines $HOME to a temp dir
	defer app.shutdown(context.Background())

	srcRoot := t.TempDir()
	for _, name := range []string{"erun-orchestrate", "erun-file-issue"} {
		dir := filepath.Join(srcRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("# "+name+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ERUN_SKILLS_DIR", srcRoot)

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("ensureOrchestratorWorkspace failed: %v", err)
	}

	home, _ := os.UserHomeDir()
	skillsRoot := filepath.Join(home, ".claude", "skills")
	for _, name := range []string{"erun-orchestrate", "erun-file-issue"} {
		if _, err := os.Stat(filepath.Join(skillsRoot, name, "SKILL.md")); err != nil {
			t.Fatalf("skill %q not installed by default: %v", name, err)
		}
	}

	// A hand-edited installed skill survives a re-run.
	edited := filepath.Join(skillsRoot, "erun-orchestrate", "SKILL.md")
	if err := os.WriteFile(edited, []byte("EDITED"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace failed: %v", err)
	}
	if data, _ := os.ReadFile(edited); string(data) != "EDITED" {
		t.Fatalf("expected in-place skill edit preserved, got %q", data)
	}
}

func installedOrchestrateSkill(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills", "erun-orchestrate", "SKILL.md")
}

// singleSkillSource points ERUN_SKILLS_DIR at a one-skill fixture and returns its SKILL.md.
func singleSkillSource(t *testing.T, body string) string {
	t.Helper()
	srcRoot := t.TempDir()
	skillDir := filepath.Join(srcRoot, "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ERUN_SKILLS_DIR", srcRoot)
	return srcMD
}

func TestOrchestratorSkillsRefreshWhenSourceChanges(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleSkillSource(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
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

func TestOrchestratorSkillsPreserveEditAcrossSourceChange(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := singleSkillSource(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)

	// The operator's edit must win over a newer source.
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

func clearSkillsOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_SKILLS_DIR", "")
}

// runFromBareLayout pins the binary outside any checkout, so only the build stamp
// can name the skills; a test binary inside the checkout would resolve them anyway.
func runFromBareLayout(t *testing.T) string {
	t.Helper()
	exeDir := filepath.Join(t.TempDir(), "dev-bin")
	if err := os.MkdirAll(exeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(exeDir, "erun-app")
	previous := runningExecutable
	runningExecutable = func() (string, error) { return exe, nil }
	t.Cleanup(func() { runningExecutable = previous })
	return exeDir
}

// stampedSkillsCheckout returns the source SKILL.md reachable only via the build stamp.
func stampedSkillsCheckout(t *testing.T, body string) string {
	t.Helper()
	clearSkillsOverride(t)
	runFromBareLayout(t)
	skillsRoot := filepath.Join(t.TempDir(), "checkout", "erun-skills", "skills")
	skillDir := filepath.Join(skillsRoot, "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	srcMD := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(srcMD, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	stampSkillsSource(t, skillsRoot)
	return srcMD
}

func stampSkillsSource(t *testing.T, dir string) {
	t.Helper()
	previous := stampedSkillsSource
	stampedSkillsSource = dir
	t.Cleanup(func() { stampedSkillsSource = previous })
}

func orchestratorTestAppWithEmits(t *testing.T) (*testApp, *capturedEmits) {
	t.Helper()
	app := orchestratorTestApp(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	return app, emits
}

// The bundle is copied out of its checkout, so only the build stamp resolves a source.
func TestOrchestratorSkillsRefreshFromBuildStampedCheckout(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())
	srcMD := stampedSkillsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
	if data, _ := os.ReadFile(installed); string(data) != "# v1\n" {
		t.Fatalf("expected the stamped checkout's skill installed, got %q", data)
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
	// The marker moves with the refresh so later launches can detect edits.
	marker, err := os.ReadFile(filepath.Join(filepath.Dir(installed), podruntime.AgentSkillMarker))
	if err != nil {
		t.Fatalf("read skill marker: %v", err)
	}
	want, err := podruntime.FileSHA256(srcMD)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(marker)) != want {
		t.Fatalf("marker = %q, want the refreshed source sha %q", strings.TrimSpace(string(marker)), want)
	}
	if notes := emits.events(uimodel.AppNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}

func TestOrchestratorSkillsPreserveEditFromBuildStampedCheckout(t *testing.T) {
	app := orchestratorTestApp(t)
	defer app.shutdown(context.Background())
	srcMD := stampedSkillsCheckout(t, "# v1\n")

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("first ensureOrchestratorWorkspace: %v", err)
	}
	installed := installedOrchestrateSkill(t)
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

// A silent failure to install reads like an unchanged skill, so it is reported once.
func TestOrchestratorSkillsReportUnresolvableSource(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearSkillsOverride(t)
	exeDir := runFromBareLayout(t)
	// A checkout not on this machine, as a binary built elsewhere carries.
	moved := filepath.Join(t.TempDir(), "checkout-that-moved", "erun-skills", "skills")
	stampSkillsSource(t, moved)

	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("the orchestrator must still launch cleanly: %v", err)
	}
	notes := emits.events(uimodel.AppNotificationEvent)
	if len(notes) != 1 {
		t.Fatalf("expected exactly one report, got %+v", notes)
	}
	payload, ok := notes[0].(uimodel.AppNotificationPayload)
	if !ok {
		t.Fatalf("unexpected payload type: %T", notes[0])
	}
	if payload.Kind != "warning" {
		t.Fatalf("kind = %q, want warning", payload.Kind)
	}
	for _, want := range []string{moved, exeDir, "ERUN_SKILLS_DIR", "build.sh"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("report does not name %q:\n%s", want, payload.Message)
		}
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "erun-orchestrate")); !os.IsNotExist(err) {
		t.Fatalf("nothing resolvable must install nothing, stat err = %v", err)
	}
	// Once: a second launch repeats it in the log only.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if got := emits.events(uimodel.AppNotificationEvent); len(got) != 1 {
		t.Fatalf("expected the report said once, got %+v", got)
	}
}

// ERUN_SKILLS_DIR means "use exactly this", even when empty.
func TestHostSkillsSourceOverrideWinsOverBuildStamp(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	stampedSkillsCheckout(t, "# stamped\n")
	override := t.TempDir()
	t.Setenv("ERUN_SKILLS_DIR", override)

	resolved, err := hostSkillsSource(stampedSkillsSource)
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
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "erun-orchestrate")); !os.IsNotExist(err) {
		t.Fatalf("an empty override must install nothing, stat err = %v", err)
	}
	if notes := emits.events(uimodel.AppNotificationEvent); len(notes) != 0 {
		t.Fatalf("an honoured override is not a failure to report, got %+v", notes)
	}
}

func TestOrchestratorSkillsResolveFromCheckoutAroundExecutable(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearSkillsOverride(t)
	stampSkillsSource(t, "")

	checkout := t.TempDir()
	skillDir := filepath.Join(checkout, "erun-skills", "skills", "erun-orchestrate")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# from the checkout\n"), 0o644); err != nil {
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
	if data, _ := os.ReadFile(installedOrchestrateSkill(t)); string(data) != "# from the checkout\n" {
		t.Fatalf("expected the surrounding checkout's skill installed, got %q", data)
	}
	if notes := emits.events(uimodel.AppNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}
