package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/command/podruntime"
)

func TestOrchestratorSkillsInstalledByDefault(t *testing.T) {
	app := orchestratorTestApp(t) // confines $HOME to a temp dir
	defer app.shutdown(context.Background())

	// Fixture skills source standing in for the repo's erun-skills/skills.
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

	// Idempotent + edit-preserving: a hand-edited installed skill survives a re-run.
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

// installedOrchestrateSkill is the path an installed erun-orchestrate SKILL.md
// lands at under the test's confined $HOME.
func installedOrchestrateSkill(t *testing.T) string {
	t.Helper()
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".claude", "skills", "erun-orchestrate", "SKILL.md")
}

// singleSkillSource writes a one-skill fixture source (erun-orchestrate) and
// points ERUN_SKILLS_DIR at it, returning the source SKILL.md path.
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

	// A newer skill ships; an untouched install must track it on next launch.
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

	// Operator edits the installed skill; then a newer source ships. The edit
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

// clearSkillsOverride removes the exact-source override so a test exercises the
// resolution a desktop actually runs with.
func clearSkillsOverride(t *testing.T) {
	t.Helper()
	t.Setenv("ERUN_SKILLS_DIR", "")
}

// runFromBareLayout puts the running binary where the desktop actually runs
// from: a directory with no checkout above it, so nothing but the build stamp
// can name the skills this build ships. Pinned rather than inherited because a
// test binary's own location is not the test's to choose — one compiled into the
// checkout would otherwise resolve the repo's own skills and quietly stop
// exercising the layout the test is about.
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

// stampedSkillsCheckout stands in for the checkout a desktop build was produced
// from, in the layout that motivated the stamp: the running binary sits nowhere
// near a source tree, so the walk up from the executable resolves nothing and
// only the build stamp names the skills this build shipped. Returns the source
// SKILL.md.
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

// stampSkillsSource sets the build stamp for one test and restores it after.
func stampSkillsSource(t *testing.T, dir string) {
	t.Helper()
	previous := buildSkillsSource
	buildSkillsSource = dir
	t.Cleanup(func() { buildSkillsSource = previous })
}

// orchestratorTestAppWithEmits captures the frontend events the app posts, so a
// test can assert what the operator is actually told.
func orchestratorTestAppWithEmits(t *testing.T) (*App, *capturedEmits) {
	t.Helper()
	app := orchestratorTestApp(t)
	emits := newCapturedEmits()
	app.SetEmitter(emits.fn())
	return app, emits
}

// TestOrchestratorSkillsRefreshFromBuildStampedCheckout covers the layout the
// desktop is actually built and run from: the bundle is copied out of its
// checkout, so nothing above the running binary names a source tree and only the
// build stamp resolves one. An installed copy erun itself wrote must track that
// source instead of freezing at whatever landed on first install.
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
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}

// TestOrchestratorSkillsPreserveEditFromBuildStampedCheckout keeps the
// operator's copy theirs in that same layout: resolving a source is not licence
// to overwrite a skill edited in place.
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

// TestOrchestratorSkillsReportUnresolvableSource locks the second half of the
// contract: when nothing resolves, the launch still succeeds and the operator is
// told once — a build that silently stops installing skills reads exactly like
// one where the skill had not changed.
func TestOrchestratorSkillsReportUnresolvableSource(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	clearSkillsOverride(t)
	exeDir := runFromBareLayout(t)
	// A checkout that is not on this machine, which is what a binary built
	// elsewhere carries.
	moved := filepath.Join(t.TempDir(), "checkout-that-moved", "erun-skills", "skills")
	stampSkillsSource(t, moved)

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
	// The report has to be actionable on its own: what the build expected, where
	// the running binary looked instead, and both recoveries.
	for _, want := range []string{moved, exeDir, "ERUN_SKILLS_DIR", "build.sh"} {
		if !strings.Contains(payload.Message, want) {
			t.Fatalf("report does not name %q:\n%s", want, payload.Message)
		}
	}
	home, _ := os.UserHomeDir()
	if _, err := os.Stat(filepath.Join(home, ".claude", "skills", "erun-orchestrate")); !os.IsNotExist(err) {
		t.Fatalf("nothing resolvable must install nothing, stat err = %v", err)
	}
	// Once: the condition belongs to the build, so a second launch repeats it in
	// the log only.
	if _, err := app.ensureOrchestratorWorkspace(); err != nil {
		t.Fatalf("second ensureOrchestratorWorkspace: %v", err)
	}
	if got := emits.events(appNotificationEvent); len(got) != 1 {
		t.Fatalf("expected the report said once, got %+v", got)
	}
}

// TestHostSkillsSourceOverrideWinsOverBuildStamp keeps ERUN_SKILLS_DIR meaning
// "use exactly this": it beats the build stamp, and an empty directory installs
// nothing rather than falling back to a source the caller did not name.
func TestHostSkillsSourceOverrideWinsOverBuildStamp(t *testing.T) {
	app, emits := orchestratorTestAppWithEmits(t)
	defer app.shutdown(context.Background())

	stampedSkillsCheckout(t, "# stamped\n")
	override := t.TempDir()
	t.Setenv("ERUN_SKILLS_DIR", override)

	resolved, err := hostSkillsSource()
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
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("an honoured override is not a failure to report, got %+v", notes)
	}
}

// TestOrchestratorSkillsResolveFromCheckoutAroundExecutable keeps the layout
// that already worked working: a binary sitting inside a checkout resolves that
// checkout's skills even with no build stamp, which is the case a packaged
// tree — or a binary built by anything other than these scripts — relies on.
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
	if data, _ := os.ReadFile(installedOrchestrateSkill(t)); string(data) != "# from the checkout\n" {
		t.Fatalf("expected the surrounding checkout's skill installed, got %q", data)
	}
	if notes := emits.events(appNotificationEvent); len(notes) != 0 {
		t.Fatalf("a resolved source must report nothing, got %+v", notes)
	}
}
