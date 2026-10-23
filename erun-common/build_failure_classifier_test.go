package eruncommon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A loaded Docker daemon can fail a test stage's Testcontainers startup before
// any test ran -- the reaper never leaves status "created", or the network it
// guards is refused. That says nothing about the change, so recording it as a
// FAILED build is refused before any platform call.
func TestRunReviewRecordBuildRefusesADockerDaemonStartupFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not be called", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	for _, detail := range []string{
		`--- FAIL: TestRoleReconciliation (0.00s) testpg: create a Docker network (the database suites need a Docker daemon): ` +
			`reaper: new reaper: wait for reaper 1a2b3c4d: unexpected container status "created"`,
		`testpg: start postgres:18.3: reaper: Error response from daemon: context deadline exceeded: failed to create network`,
	} {
		_, err := RunReviewRecordBuild(Context{}, pushNoticeStoreWithAlias(srv.URL), "", ReviewRecordBuildParams{
			ReviewID: "review-1", CommitID: "abc", Version: "1.0.0", Successful: false, FailureDetail: detail,
		}, pushNoticeDeps(t))
		if err == nil || !strings.Contains(err.Error(), "re-run the build") {
			t.Errorf("expected %q to be refused with the remedy, got %v", detail, err)
		}
	}
	if calls.Load() != 0 {
		t.Errorf("expected the refusal before any platform call, got %d calls", calls.Load())
	}
}

// A container failing for a reason of its own is still a verdict on the change.
func TestAnOrdinaryContainerFailureIsStillRecordable(t *testing.T) {
	detail := `--- FAIL: TestMigrations (2.10s) apply erun-backend-db migrations: atlas exited 1`
	if err := ensureNotKnownInfrastructureBuildFailure(false, detail); err != nil {
		t.Errorf("expected %q to stay recordable as FAILED, got %v", detail, err)
	}
}

// A test that creates its own Docker network can fail for a reason of its own,
// such as a pinned subnet overlapping an existing one. That is a verdict on the
// change, not the Testcontainers reaper failing to start, so it stays
// recordable as FAILED.
func TestASubnetOverlapIsStillRecordable(t *testing.T) {
	detail := `--- FAIL: TestPinnedSubnet (0.40s) failed to create network erun_test_net: ` +
		`Error response from daemon: Pool overlaps with other one on this address space`
	if err := ensureNotKnownInfrastructureBuildFailure(false, detail); err != nil {
		t.Errorf("expected %q to stay recordable as FAILED, got %v", detail, err)
	}
}

// The refusal names every kind of infrastructure the signatures cover,
// including the Docker daemon the reaper signatures belong to.
func TestTheRefusalNamesTheDockerDaemon(t *testing.T) {
	err := ensureNotKnownInfrastructureBuildFailure(false, `reaper: new reaper: wait for reaper 1a2b3c4d: unexpected container status "created"`)
	if err == nil || !strings.Contains(err.Error(), "network, registry, or Docker daemon") {
		t.Errorf("expected the refusal to name the network, registry, or Docker daemon, got %v", err)
	}
}
