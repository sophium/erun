package review

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// The platform lands a review MERGED against a plain successful build; there is
// no GATE kind.
func TestRunReviewRecordBuildRecordsARecordedBuildWithItsVersion(t *testing.T) {
	var body map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/reviews/review-1/builds" {
			http.Error(w, "unexpected "+r.Method+" "+r.URL.Path, http.StatusNotFound)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`{"buildId":"build-1","successful":true,"commitId":"abc","version":"1.0.0"}`))
	}))
	t.Cleanup(srv.Close)

	build, err := RunReviewRecordBuild(core.Context{}, pushNoticeStoreWithAlias(srv.URL), "", model.ReviewRecordBuildParams{
		ReviewID: "review-1", CommitID: "abc", Version: "1.0.0", Successful: true,
	}, pushNoticeDeps(t))
	if err != nil {
		t.Fatalf("RunReviewRecordBuild: %v", err)
	}
	if build.BuildID != "build-1" {
		t.Fatalf("build = %+v, want the platform's recorded build", build)
	}
	if body["kind"] != "RECORDED" || body["version"] != "1.0.0" || body["commitId"] != "abc" || body["successful"] != true {
		t.Fatalf("request body = %v, want a successful RECORDED build of abc at 1.0.0", body)
	}
}
