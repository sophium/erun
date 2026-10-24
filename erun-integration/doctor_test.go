package integration

import (
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

func TestDoctor(t *testing.T) {
	t.Parallel()
	t.Run("help", testDoctorHelp)
	doctorDeployDiagnosisScenarios(t)
	doctorPruneScenarios(t)
	doctorInRuntimeScenarios(t)
	doctorInspectConfigScenarios(t)
	doctorRepairConfigScenarios(t)
	doctorRemoteInitScenarios(t)
	doctorJetBrainsScenarios(t)
	doctorCredentialScenarios(t)
	doctorInstallationScenarios(t)
	doctorSyncConfigScenarios(t)
}

func testDoctorHelp(t *testing.T) {
	setup := env.New(t)
	result := erun.Run(t, []string{"doctor", "--help"}, erun.RunOptions{Cwd: setup.Cwd, Env: setup.Env()})
	if result.ExitCode != 0 {
		t.Fatalf("exit %d: %s", result.ExitCode, result.Combined)
	}
	golden.Equal(t, "doctor/help", normalize.Apply(result.Combined))
}

func seedRemoteInitMarker(t *testing.T, setup env.Setup, tenant, environment, body string) {
	t.Helper()
	markerDir := filepath.Join(setup.Home, ".erun", tenant, environment)
	if err := os.MkdirAll(markerDir, 0o700); err != nil {
		t.Fatalf("mkdir marker dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(markerDir, "bootstrap.yaml"), []byte(body), 0o600); err != nil {
		t.Fatalf("write marker: %v", err)
	}
}

// stubDoctorHelmStatus reports the runtime release in releaseStatus.
func stubDoctorHelmStatus(t *testing.T, stubsDir, releaseStatus string) {
	t.Helper()
	script := strings.Join([]string{
		`case "$1" in`,
		`  status) printf '%s\n' 'NAME: team-devops' 'STATUS: ` + releaseStatus + `' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "helm", script)
}

// stubDoctorPodRequestsArm answers the `-o json` pod read. It comes first because
// the table arm also matches " get pods ".
func stubDoctorPodRequestsArm() string {
	return `  *" get pods "*"-o json"*) printf '%s\n' '` + stubUsagePodRequestsList + `' ;;`
}

// stubDoctorKubectl stubs every kubectl surface doctor touches; waitArm overrides
// the deployment wait to simulate cluster failures.
func stubDoctorKubectl(t *testing.T, stubsDir, waitArm string) {
	t.Helper()
	if waitArm == "" {
		waitArm = ":"
	}
	script := strings.Join([]string{
		`case "$*" in`,
		stubDoctorPodRequestsArm(),
		`  *" get pods "*) printf '%s\n' 'NAME                READY   STATUS    RESTARTS' 'team-devops-pod-1   2/2     Running   0' ;;`,
		`  *" get namespaces "*) printf 'namespace/team-dev\n' ;;`,
		`  *" wait "*) ` + waitArm + ` ;;`,
		`  *" delete "*) printf 'secret/sh.helm.release deleted\n' ;;`,
		`  *"df -h /var/lib/docker"*) printf '%s\n' 'Filesystem  Size  Used  Avail  Mounted on' 'overlay     100G  20G   80G    /var/lib/docker' ;;`,
		`  *"docker image prune"*) printf '%s\n' 'Total reclaimed space: 2GB' ;;`,
		`  *"docker builder prune"*) printf '%s\n' 'Total: 3GB' ;;`,
		`  *"docker container prune"*) printf '%s\n' 'Total reclaimed space: 1GB' ;;`,
		`esac`,
		`exit 0`,
	}, "\n")
	fixture.StubBinaryWithScript(t, stubsDir, "kubectl", script)
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
