package podruntime

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAssetFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func assertAssetFile(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if string(data) != want {
		t.Fatalf("%s = %q, want %q", path, data, want)
	}
}

// TestInstallOrRefreshSkillsTracksTheSourceAndKeepsOperatorEdits walks one skill
// through every state an environment's home volume can hold it in: absent,
// erun's unmodified copy of an older source, an operator's in-place edit, and a
// copy installed before markers existed.
func TestInstallOrRefreshSkillsTracksTheSourceAndKeepsOperatorEdits(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	skill := filepath.Join(src, "erun-build-env")
	installed := filepath.Join(dest, "erun-build-env")
	marker := filepath.Join(installed, AgentSkillMarker)

	writeAssetFile(t, filepath.Join(skill, "SKILL.md"), "v1 body\n")
	writeAssetFile(t, filepath.Join(skill, "templates", "reference.md"), "ref v1\n")
	result := installSkillsForTest(t, src, dest)
	if strings.Join(result.Installed, ",") != "erun-build-env" {
		t.Fatalf("fresh install should report the skill installed, got %+v", result)
	}
	assertAssetFile(t, filepath.Join(installed, "SKILL.md"), "v1 body\n")
	assertAssetFile(t, filepath.Join(installed, "templates", "reference.md"), "ref v1\n")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a fresh install must record its marker: %v", err)
	}

	writeAssetFile(t, filepath.Join(skill, "SKILL.md"), "v2 body\n")
	writeAssetFile(t, filepath.Join(skill, "templates", "reference.md"), "ref v2\n")
	if result = installSkillsForTest(t, src, dest); len(result.Refreshed) != 1 {
		t.Fatalf("an unmodified install must follow a changed source: %+v", result)
	}
	assertAssetFile(t, filepath.Join(installed, "SKILL.md"), "v2 body\n")
	assertAssetFile(t, filepath.Join(installed, "templates", "reference.md"), "ref v2\n")

	writeAssetFile(t, filepath.Join(installed, "SKILL.md"), "operator edit\n")
	writeAssetFile(t, filepath.Join(skill, "SKILL.md"), "v3 body\n")
	if result = installSkillsForTest(t, src, dest); len(result.Preserved) != 1 {
		t.Fatalf("an in-place edit must be preserved: %+v", result)
	}
	assertAssetFile(t, filepath.Join(installed, "SKILL.md"), "operator edit\n")

	writeAssetFile(t, filepath.Join(installed, "SKILL.md"), "ancient body\n")
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	writeAssetFile(t, filepath.Join(skill, "SKILL.md"), "v4 body\n")
	installSkillsForTest(t, src, dest)
	assertAssetFile(t, filepath.Join(installed, "SKILL.md"), "v4 body\n")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a legacy copy must be adopted with a marker: %v", err)
	}
}

func installSkillsForTest(t *testing.T, src, dest string) AgentAssetsInstallResult {
	t.Helper()
	result, err := InstallOrRefreshSkills(src, dest)
	if err != nil {
		t.Fatalf("install skills: %v", err)
	}
	return result
}

// The image bakes skills 0755 so the unprivileged user can enter them; what is
// installed is always ordinary 0644 files in 0755 directories.
func TestInstallOrRefreshSkillsNormalisesModes(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	writeAssetFile(t, filepath.Join(src, "s", "SKILL.md"), "body\n")
	if err := os.Chmod(filepath.Join(src, "s", "SKILL.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallOrRefreshSkills(src, dest); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dest, "s", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("installed skill file mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestInstallOrRefreshAgentsTracksTheSourceAndKeepsOperatorEdits(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "agents")
	source := filepath.Join(src, "erun-builder.md")
	installed := filepath.Join(dest, "erun-builder.md")
	marker := installed + AgentFileMarkerSuffix
	writeAssetFile(t, filepath.Join(src, "notes.txt"), "not an agent\n")

	writeAssetFile(t, source, "v1\n")
	if _, err := InstallOrRefreshAgents(src, dest); err != nil {
		t.Fatalf("fresh install: %v", err)
	}
	assertAssetFile(t, installed, "v1\n")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("a fresh install must record its marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("only <name>.md files are agents, stat err = %v", err)
	}

	writeAssetFile(t, source, "v2\n")
	if _, err := InstallOrRefreshAgents(src, dest); err != nil {
		t.Fatal(err)
	}
	assertAssetFile(t, installed, "v2\n")

	writeAssetFile(t, installed, "operator edit\n")
	writeAssetFile(t, source, "v3\n")
	if _, err := InstallOrRefreshAgents(src, dest); err != nil {
		t.Fatal(err)
	}
	assertAssetFile(t, installed, "operator edit\n")

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	writeAssetFile(t, source, "v4\n")
	if _, err := InstallOrRefreshAgents(src, dest); err != nil {
		t.Fatal(err)
	}
	assertAssetFile(t, installed, "v4\n")
}

func TestInstallOrRefreshSkillsReportsAnUnreadableSource(t *testing.T) {
	_, err := InstallOrRefreshSkills(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "read skills source") {
		t.Fatalf("expected the unreadable source to be named, got %v", err)
	}
}
