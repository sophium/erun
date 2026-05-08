package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	common "github.com/sophium/erun/erun-common"
	jetbrainsconfig "github.com/sophium/erun/internal/jetbrainsconfig"
)

// The dry-run trace for `doctor --prune-images` is covered by the
// integration suite (erun-integration/doctor_test.go). The cleanup-prompt
// real-execution case from the previous iteration was driven via a kubectl
// PATH stub, which is now disallowed (see AGENTS.md: stubs are a code
// smell), so the assertion below stays as the only place that confirms the
// `doctor --repair-jetbrains-gateway` flag wires through the cmd layer to
// `jetbrainsconfig.ClearRecentProjectLatestUsedIDE`. The mutation logic
// itself is unit-tested in internal/jetbrainsconfig.

func TestDoctorCommandRepairsJetBrainsGatewayMetadata(t *testing.T) {
	setupRootCmdTestConfigHome(t)

	prevHome := ideUserHomeDir
	prevGlob := ideGlob
	t.Cleanup(func() {
		ideUserHomeDir = prevHome
		ideGlob = prevGlob
	})

	projectRoot := filepath.Join(t.TempDir(), "petios")
	requireNoError(t, os.MkdirAll(projectRoot, 0o755), "mkdir project root")
	requireNoError(t, common.SaveERunConfig(common.ERunConfig{DefaultTenant: "petios"}), "SaveERunConfig failed")
	requireNoError(t, common.SaveTenantConfig(common.TenantConfig{Name: "petios", ProjectRoot: projectRoot, DefaultEnvironment: "rihards"}), "SaveTenantConfig failed")
	if err := common.SaveEnvConfig("petios", common.EnvConfig{
		Name:              "rihards",
		RepoPath:          projectRoot,
		KubernetesContext: "cluster-dev",
		SSHD: common.SSHDConfig{
			Enabled:   true,
			LocalPort: 17422,
		},
	}); err != nil {
		t.Fatalf("SaveEnvConfig failed: %v", err)
	}

	root := t.TempDir()
	optionsDir := filepath.Join(root, "JetBrains", "IntelliJIdea2025.3", "options")
	requireNoError(t, os.MkdirAll(optionsDir, 0o700), "mkdir options dir")
	configID := jetbrainsconfig.StableConfigID("erun-petios-rihards")
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
                <option name="date" value="1777477119934"></option>
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

	ideUserHomeDir = func() (string, error) { return root, nil }
	ideGlob = func(pattern string) ([]string, error) {
		if strings.Contains(pattern, "IntelliJIdea*") {
			return []string{optionsDir}, nil
		}
		return nil, nil
	}

	cmd := newTestRootCmd(testRootDeps{})
	stdout := new(bytes.Buffer)
	stderr := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(stderr)
	cmd.SetArgs([]string{"doctor", "petios", "rihards", "--repair-jetbrains-gateway"})

	requireNoError(t, cmd.Execute(), "Execute failed")

	output := stdout.String()
	for _, want := range []string{
		"Target: petios/rihards",
		"Running: Clear cached JetBrains Gateway backend metadata",
		"Cleared cached JetBrains Gateway backend metadata",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected output to contain %q, got:\n%s", want, output)
		}
	}
	recent, err := os.ReadFile(recentPath)
	if err != nil {
		t.Fatalf("read %s: %v", recentPath, err)
	}
	if strings.Contains(string(recent), "latestUsedIde") || strings.Contains(string(recent), "pathToIde") {
		t.Fatalf("expected cached IDE metadata to be removed, got:\n%s", recent)
	}
}
