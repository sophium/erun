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
	// JetBrains Gateway repair against a seeded
	// sshRecentConnections.v2.xml carrying a latestUsedIde block (the
	// state IntelliJ leaves behind after a remote session; shape
	// mirrors open_test.go's gateway scenario). Pass 1 (--dry-run)
	// must preview the clear and leave the XML untouched; pass 2
	// (real) must route through
	// jetbrainsconfig.ClearRecentProjectLatestUsedIDE and strip the
	// block while preserving the rest of the project entry. The file
	// mutation is unreachable from dry-run by design.
	// ERUN_HOST_OS_OVERRIDE=linux pins the options-dir resolution to
	// ~/.config/JetBrains (per erun-integration/AGENTS.md, platform-
	// dependent goldens).
	setup := env.New(t)
	fixture.SeedTenantEnv(t, setup, "team", "dev")
	configID := jetBrainsStableConfigID("erun-team-dev")
	projectPath := "/home/erun/git/" + filepath.Base(setup.Cwd)
	idePath := filepath.Join(setup.Home, "jetbrains-backends", "ideaIU-243.22562.222")
	xmlPath := seedJetBrainsRecentProject(t, setup, configID, projectPath, idePath)
	// The real pass execs kubectl for the read-only pod sections (the
	// resources reading among them); the scrubbed PATH means an undeclared
	// binary would otherwise be answered by whatever the host has
	// installed.
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
	// --repair-jetbrains-gateway with no JetBrains install at all:
	// the options-dir resolution fails, the repair finds nothing to
	// clear, and doctor must say so and stop (covering the not-found
	// arm of runSelectedJetBrainsGatewayRepair) instead of falling
	// through to the kubectl-driven cleanup actions.
	//
	// Only kubectl is declared: every read-only pod section before the
	// repair (the resources reading among them) reaches for it, and this
	// suite's scrubbed PATH refuses to let a scenario depend on whatever
	// the host happens to have installed. helm stays undeclared on purpose
	// -- its own diagnosis already degrades to an unreadable release
	// without this scenario's golden having to carry a stubbed one.
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

// jetBrainsStableConfigID duplicates jetbrainsconfig.StableConfigID because that
// internal package cannot be imported across modules; the seeded XML must carry
// the same configId or FindRecentProject never matches.
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

// seedJetBrainsRecentProject writes the linux-side IntelliJ options dir
// with a sshRecentConnections.v2.xml whose single recent project
// carries a latestUsedIde block — the exact state `erun open
// --intellij` plus a finished Gateway session leaves behind (shape
// cribbed from open_test.go's gateway scenario). Returns the XML path
// so callers can assert the on-disk mutation.
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
