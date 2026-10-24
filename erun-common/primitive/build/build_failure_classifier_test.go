package build

import (
	"strings"
	"testing"
)

func TestAnOrdinaryContainerFailureIsStillRecordable(t *testing.T) {
	detail := `--- FAIL: TestMigrations (2.10s) apply erun-backend-db migrations: atlas exited 1`
	if err := EnsureNotKnownInfrastructureBuildFailure(false, detail); err != nil {
		t.Errorf("expected %q to stay recordable as FAILED, got %v", detail, err)
	}
}

func TestASubnetOverlapIsStillRecordable(t *testing.T) {
	detail := `--- FAIL: TestPinnedSubnet (0.40s) failed to create network erun_test_net: ` +
		`Error response from daemon: Pool overlaps with other one on this address space`
	if err := EnsureNotKnownInfrastructureBuildFailure(false, detail); err != nil {
		t.Errorf("expected %q to stay recordable as FAILED, got %v", detail, err)
	}
}

func TestTheRefusalNamesTheDockerDaemon(t *testing.T) {
	err := EnsureNotKnownInfrastructureBuildFailure(false, `reaper: new reaper: wait for reaper 1a2b3c4d: unexpected container status "created"`)
	if err == nil || !strings.Contains(err.Error(), "network, registry, or Docker daemon") {
		t.Errorf("expected the refusal to name the network, registry, or Docker daemon, got %v", err)
	}
}
