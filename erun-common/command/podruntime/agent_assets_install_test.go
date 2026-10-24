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

// install-skills drives install, refresh and preserve; a pre-marker copy is adopted.
func TestInstallOrRefreshSkillsAdoptsALegacyCopy(t *testing.T) {
	src := t.TempDir()
	dest := t.TempDir()
	installed := filepath.Join(dest, "erun-build-env")
	writeAssetFile(t, filepath.Join(src, "erun-build-env", "SKILL.md"), "v4 body\n")
	writeAssetFile(t, filepath.Join(installed, "SKILL.md"), "ancient body\n")
	installSkillsForTest(t, src, dest)
	assertAssetFile(t, filepath.Join(installed, "SKILL.md"), "v4 body\n")
	if _, err := os.Stat(filepath.Join(installed, AgentSkillMarker)); err != nil {
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

// install-skills drives install, refresh and preserve; a pre-marker copy is adopted
// and only <name>.md files are agents.
func TestInstallOrRefreshAgentsAdoptsALegacyCopy(t *testing.T) {
	src := t.TempDir()
	dest := filepath.Join(t.TempDir(), "agents")
	installed := filepath.Join(dest, "erun-builder.md")
	writeAssetFile(t, filepath.Join(src, "notes.txt"), "not an agent\n")
	writeAssetFile(t, filepath.Join(src, "erun-builder.md"), "v4\n")
	writeAssetFile(t, installed, "ancient\n")
	if _, err := InstallOrRefreshAgents(src, dest); err != nil {
		t.Fatal(err)
	}
	assertAssetFile(t, installed, "v4\n")
	if _, err := os.Stat(installed + AgentFileMarkerSuffix); err != nil {
		t.Fatalf("a legacy copy must be adopted with a marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "notes.txt")); !os.IsNotExist(err) {
		t.Fatalf("only <name>.md files are agents, stat err = %v", err)
	}
}

func TestInstallOrRefreshSkillsReportsAnUnreadableSource(t *testing.T) {
	_, err := InstallOrRefreshSkills(filepath.Join(t.TempDir(), "missing"), t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "read skills source") {
		t.Fatalf("expected the unreadable source to be named, got %v", err)
	}
}
