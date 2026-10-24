package review

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// A Docker daemon startup failure says nothing about the change, so it must not
// be recorded as a FAILED build.
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
		_, err := RunReviewRecordBuild(core.Context{}, pushNoticeStoreWithAlias(srv.URL), "", ReviewRecordBuildParams{
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
