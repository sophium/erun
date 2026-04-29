package jetbrainsconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStableConfigIDDeterministic(t *testing.T) {
	first := StableConfigID("erun-erun-remote")
	second := StableConfigID("erun-erun-remote")
	if first != second {
		t.Fatalf("expected deterministic config ID, got %q and %q", first, second)
	}
	if len(first) != 36 {
		t.Fatalf("unexpected config ID format: %q", first)
	}
}

func TestUpsertOptionsFilesCreatesJetBrainsSSHProjectFiles(t *testing.T) {
	optionsDir := t.TempDir()
	entry := ProjectEntry{
		ConfigID:       StableConfigID("erun-erun-remote"),
		HostAlias:      "erun-erun-remote",
		User:           "erun",
		IdentityFile:   "/Users/test/.ssh/id_ed25519",
		ProjectPath:    "/home/erun/git/erun",
		Port:           62222,
		ProductCode:    "IU",
		TimestampMilli: 1776697818596,
	}

	if err := UpsertOptionsFiles(optionsDir, entry); err != nil {
		t.Fatalf("UpsertOptionsFiles failed: %v", err)
	}

	assertFileContains(t, filepath.Join(optionsDir, "sshConfigs.xml"),
		`host="erun-erun-remote"`,
		`id="`+entry.ConfigID+`"`,
		`keyPath="/Users/test/.ssh/id_ed25519"`,
		`port="62222"`,
		`username="erun"`,
	)
	assertFileContains(t, filepath.Join(optionsDir, "sshRecentConnectionsHost.xml"),
		`<option value="`+entry.ConfigID+`"></option>`,
	)
	assertFileContains(t, filepath.Join(optionsDir, "sshRecentConnections.v2.xml"),
		`<option name="configId" value="`+entry.ConfigID+`"></option>`,
		`<option name="projectPath" value="/home/erun/git/erun"></option>`,
		`<option name="productCode" value="IU"></option>`,
	)
}

func TestUpsertOptionsFilesUpdatesExistingEntriesWithoutDuplicates(t *testing.T) {
	optionsDir := t.TempDir()
	first := ProjectEntry{
		ConfigID:       StableConfigID("erun-erun-remote"),
		HostAlias:      "erun-erun-remote",
		User:           "erun",
		IdentityFile:   "/Users/test/.ssh/id_ed25519",
		ProjectPath:    "/home/erun/git/erun",
		Port:           62222,
		ProductCode:    "IU",
		TimestampMilli: 1,
	}
	second := first
	second.ProjectPath = "/home/erun/git/erun-next"
	second.TimestampMilli = 2

	if err := UpsertOptionsFiles(optionsDir, first); err != nil {
		t.Fatalf("first UpsertOptionsFiles failed: %v", err)
	}
	if err := UpsertOptionsFiles(optionsDir, second); err != nil {
		t.Fatalf("second UpsertOptionsFiles failed: %v", err)
	}

	configs := readFile(t, filepath.Join(optionsDir, "sshConfigs.xml"))
	if strings.Count(configs, `host="erun-erun-remote"`) != 1 {
		t.Fatalf("expected one sshConfig entry, got:\n%s", configs)
	}

	recent := readFile(t, filepath.Join(optionsDir, "sshRecentConnections.v2.xml"))
	if strings.Count(recent, `<option name="configId" value="`+first.ConfigID+`"></option>`) != 1 {
		t.Fatalf("expected one configId entry, got:\n%s", recent)
	}
	for _, want := range []string{
		`<option name="projectPath" value="/home/erun/git/erun"></option>`,
		`<option name="projectPath" value="/home/erun/git/erun-next"></option>`,
	} {
		if !strings.Contains(recent, want) {
			t.Fatalf("expected recent projects to contain %q, got:\n%s", want, recent)
		}
	}
}

func TestUpsertOptionsFilesPreservesLatestUsedIDE(t *testing.T) {
	optionsDir := t.TempDir()
	configID := StableConfigID("erun-petios-rihards-develop")
	recentPath := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	if err := os.WriteFile(recentPath, []byte(`<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="`+configID+`"></option>
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="date" value="1777362254961"></option>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="261.23567.71"></option>
                    <option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>
                    <option name="productCode" value="IU"></option>
                  </RecentProjectInstalledIde>
                </option>
                <option name="productCode" value="IU"></option>
                <option name="projectPath" value="/home/erun/git/petios"></option>
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`), 0o600); err != nil {
		t.Fatalf("write recent projects: %v", err)
	}

	err := UpsertOptionsFiles(optionsDir, ProjectEntry{
		ConfigID:       configID,
		HostAlias:      "erun-petios-rihards-develop",
		User:           "erun",
		IdentityFile:   "/Users/test/.ssh/id_ed25519",
		ProjectPath:    "/home/erun/git/petios",
		Port:           17422,
		ProductCode:    "IU",
		TimestampMilli: 1777362255000,
	})
	if err != nil {
		t.Fatalf("UpsertOptionsFiles failed: %v", err)
	}

	assertFileContains(t, recentPath,
		`<option name="latestUsedIde">`,
		`<option name="buildNumber" value="261.23567.71"></option>`,
		`<option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>`,
		`<option name="date" value="1777362255000"></option>`,
	)
}

func TestFindRecentProjectReturnsLatestUsedIDE(t *testing.T) {
	optionsDir := t.TempDir()
	configID := StableConfigID("erun-petios-rihards-develop")
	if err := os.WriteFile(filepath.Join(optionsDir, "sshRecentConnections.v2.xml"), []byte(`<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="`+configID+`"></option>
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="date" value="1777362254961"></option>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="261.23567.71"></option>
                    <option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>
                    <option name="productCode" value="IU"></option>
                  </RecentProjectInstalledIde>
                </option>
                <option name="productCode" value="IU"></option>
                <option name="projectPath" value="/home/erun/git/petios"></option>
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`), 0o600); err != nil {
		t.Fatalf("write recent projects: %v", err)
	}

	got, found, err := FindRecentProject(optionsDir, configID, "/home/erun/git/petios")
	if err != nil {
		t.Fatalf("FindRecentProject failed: %v", err)
	}
	if !found {
		t.Fatal("expected recent project to be found")
	}
	if got.ConfigID != configID || got.ProjectPath != "/home/erun/git/petios" || got.ProductCode != "IU" {
		t.Fatalf("unexpected recent project: %+v", got)
	}
	if got.LatestUsedIDE.BuildNumber != "261.23567.71" ||
		got.LatestUsedIDE.PathToIDE != "/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64" ||
		got.LatestUsedIDE.ProductCode != "IU" {
		t.Fatalf("unexpected latest IDE metadata: %+v", got.LatestUsedIDE)
	}
}

func TestClearRecentProjectLatestUsedIDERemovesBackendMetadata(t *testing.T) {
	optionsDir := t.TempDir()
	configID := StableConfigID("erun-petios-rihards")
	recentPath := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	if err := os.WriteFile(recentPath, []byte(`<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="`+configID+`"></option>
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="date" value="1777362254961"></option>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="261.23567.71"></option>
                    <option name="pathToIde" value="/home/erun/.cache/JetBrains/RemoteDev/dist/fd6f0251cd1fc_idea-261.23567.71-aarch64"></option>
                    <option name="productCode" value="IU"></option>
                  </RecentProjectInstalledIde>
                </option>
                <option name="productCode" value="IU"></option>
                <option name="projectPath" value="/home/erun/git/petios"></option>
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`), 0o600); err != nil {
		t.Fatalf("write recent projects: %v", err)
	}

	changed, err := ClearRecentProjectLatestUsedIDE(optionsDir, configID, "/home/erun/git/petios")
	if err != nil {
		t.Fatalf("ClearRecentProjectLatestUsedIDE failed: %v", err)
	}
	if !changed {
		t.Fatal("expected metadata to be changed")
	}

	recent := readFile(t, recentPath)
	if strings.Contains(recent, "latestUsedIde") || strings.Contains(recent, "pathToIde") {
		t.Fatalf("expected latest IDE metadata to be removed, got:\n%s", recent)
	}
	if !strings.Contains(recent, `<option name="projectPath" value="/home/erun/git/petios"></option>`) {
		t.Fatalf("expected project metadata to remain, got:\n%s", recent)
	}
}

func assertFileContains(t *testing.T, path string, wants ...string) {
	t.Helper()
	data := readFile(t, path)
	for _, want := range wants {
		if !strings.Contains(data, want) {
			t.Fatalf("expected %s to contain %q, got:\n%s", path, want, data)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
