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

// seedRemoteInitMarker writes a bootstrap marker under the
// per-tenant/per-environment path doctor's in-runtime inspection reads.
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

// stubDoctorHelmStatus stubs `helm status` to report the runtime
// release in the given state; every other helm invocation succeeds
// silently. The status string is the decision input
// RecommendedDeployRecovery branches on.
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

// stubDoctorKubectl stubs every kubectl surface the real-run doctor
// cleanup path touches: the pod listing for the diagnosis, the
// deployment wait (overridable via waitArm to simulate cluster
// failures), the pending-helm lock delete, the namespace probe the
// failure diagnostic runs, and the dind exec scripts for inspection and
// the three prune actions (matched on their distinctive docker lines).
// stubDoctorPodRequestsArm answers the reservation reading's `-o json` pod read
// from the same list the usage scenarios use (stubUsagePodRequestsList), while
// the table answer it precedes keeps serving the pod listing that parses a
// table. Ordered first because both match " get pods ": the table arm must
// never win the JSON read.
func stubDoctorPodRequestsArm() string {
	return `  *" get pods "*"-o json"*) printf '%s\n' '` + stubUsagePodRequestsList + `' ;;`
}

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
