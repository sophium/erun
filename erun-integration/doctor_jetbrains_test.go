package integration

import (
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-integration/internal/env"
	"github.com/sophium/erun/erun-integration/internal/erun"
	"github.com/sophium/erun/erun-integration/internal/fixture"
	"github.com/sophium/erun/erun-integration/internal/golden"
	"github.com/sophium/erun/erun-integration/internal/normalize"
)

func doctorJetBrainsScenarios(t *testing.T) {
	t.Run("repair_jetbrains_gateway_dry_run_then_real_run", testDoctorRepairJetbrainsGatewayDryRunThenRealRun)
	t.Run("repair_jetbrains_gateway_no_metadata", testDoctorRepairJetbrainsGatewayNoMetadata)
}

func testDoctorRepairJetbrainsGatewayDryRunThenRealRun(t *testing.T) {
	// Dry-run previews the clear and leaves the XML untouched; the real pass strips
	// latestUsedIde. HOST_OS_OVERRIDE=linux pins the options dir to ~/.config/JetBrains.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	configID := jetBrainsStableConfigID("erun-team-dev")
	projectPath := "/home/erun/git/" + filepath.Base(setup.Cwd)
	idePath := filepath.Join(setup.Home, "jetbrains-backends", "ideaIU-243.22562.222")
	xmlPath := seedJetBrainsRecentProject(t, setup, configID, projectPath, idePath)
	// The PATH is scrubbed, so kubectl must be stubbed for the pod sections.
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)

	dryRun := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway", "--dry-run"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if dryRun.ExitCode != 0 {
		t.Fatalf("dry-run exit %d: %s", dryRun.ExitCode, dryRun.Combined)
	}
	golden.Equal(t, "doctor/repair_jetbrains_gateway_dry_run", normalize.Apply(dryRun.Combined))
	if body := readFileForTest(t, xmlPath); !strings.Contains(body, "latestUsedIde") {
		t.Fatalf("expected dry-run to leave latestUsedIde in place, got:\n%s", body)
	}

	realRun := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if realRun.ExitCode != 0 {
		t.Fatalf("real-run exit %d: %s", realRun.ExitCode, realRun.Combined)
	}
	golden.Equal(t, "doctor/repair_jetbrains_gateway_real_run", normalize.Apply(realRun.Combined))
	body := readFileForTest(t, xmlPath)
	if strings.Contains(body, "latestUsedIde") {
		t.Errorf("expected real run to clear latestUsedIde, got:\n%s", body)
	}
	if !strings.Contains(body, projectPath) {
		t.Errorf("expected real run to preserve the project entry for %s, got:\n%s", projectPath, body)
	}
}

func testDoctorRepairJetbrainsGatewayNoMetadata(t *testing.T) {
	// With no JetBrains install, doctor reports nothing to clear and stops. helm
	// stays unstubbed: its diagnosis degrades on its own.
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	stubs := filepath.Join(setup.Cwd, "stubs")
	stubDoctorKubectl(t, stubs, "")
	envVars := append(setup.Env(), "ERUN_HOST_OS_OVERRIDE=linux")
	envVars = append(envVars, fixture.StubEnv(stubs, "kubectl")...)
	result := erun.Run(t, []string{"doctor", "team", "dev", "--repair-jetbrains-gateway"}, erun.RunOptions{Cwd: setup.Cwd, Env: envVars})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/repair_jetbrains_gateway_no_metadata", normalize.Apply(result.Combined))
}

// jetBrainsStableConfigID duplicates jetbrainsconfig.StableConfigID, which is internal
// to another module; the configId must match for FindRecentProject.
func jetBrainsStableConfigID(hostAlias string) string {
	sum := sha1.Sum([]byte(strings.TrimSpace(hostAlias)))
	b := sum[:16]
	b[6] = (b[6] & 0x0f) | 0x50
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf(
		"%02x%02x%02x%02x-%02x%02x-%02x%02x-%02x%02x-%02x%02x%02x%02x%02x%02x",
		b[0], b[1], b[2], b[3],
		b[4], b[5],
		b[6], b[7],
		b[8], b[9],
		b[10], b[11], b[12], b[13], b[14], b[15],
	)
}

// seedJetBrainsRecentProject writes a linux-side sshRecentConnections.v2.xml whose
// project carries latestUsedIde, and returns its path.
func seedJetBrainsRecentProject(t *testing.T, setup env.Setup, configID, projectPath, idePath string) string {
	t.Helper()
	optionsDir := filepath.Join(setup.ConfigHome, "JetBrains", "IntelliJIdea2024.3", "options")
	if err := os.MkdirAll(optionsDir, 0o755); err != nil {
		t.Fatalf("mkdir IntelliJ options: %v", err)
	}
	body := `<application>
  <component name="SshLocalRecentConnectionsManager">
    <option name="connections">
      <list>
        <LocalRecentConnectionState>
          <option name="configId" value="` + configID + `" />
          <option name="projects">
            <list>
              <RecentProjectState>
                <option name="latestUsedIde">
                  <RecentProjectInstalledIde>
                    <option name="buildNumber" value="243.22562.222" />
                    <option name="pathToIde" value="` + idePath + `" />
                    <option name="productCode" value="IU" />
                  </RecentProjectInstalledIde>
                </option>
                <option name="date" value="1748000000000" />
                <option name="productCode" value="IU" />
                <option name="projectPath" value="` + projectPath + `" />
              </RecentProjectState>
            </list>
          </option>
        </LocalRecentConnectionState>
      </list>
    </option>
  </component>
</application>
`
	path := filepath.Join(optionsDir, "sshRecentConnections.v2.xml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write sshRecentConnections.v2.xml: %v", err)
	}
	return path
}
