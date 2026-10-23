package eruncommon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sophium/erun/erun-common/base/core"
)

// Every build recorded against a review is RECORDED and carries its version:
// the platform lands a review MERGED against a plain successful build of the
// merge commit, and there is no GATE kind for a new build.
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

	build, err := RunReviewRecordBuild(core.Context{}, pushNoticeStoreWithAlias(srv.URL), "", ReviewRecordBuildParams{
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

func TestRunReviewRecordBuildRequiresAVersionBeforeAnyPlatformCall(t *testing.T) {
	_, err := RunReviewRecordBuild(core.Context{}, nil, "", ReviewRecordBuildParams{ReviewID: "review-1", CommitID: "abc", Successful: true}, CloudDependencies{})
	if err == nil || !strings.Contains(err.Error(), "version is required") {
		t.Fatalf("err = %v, want the missing-version refusal", err)
	}
}

// A failed build whose detail is a registry or network signature says nothing
// about the change, so it is refused for every recorded failure -- the review
// stays where it is and the remedy is to re-run the build.
func TestRunReviewRecordBuildRefusesAKnownInfrastructureFailure(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not be called", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)

	_, err := RunReviewRecordBuild(core.Context{}, pushNoticeStoreWithAlias(srv.URL), "", ReviewRecordBuildParams{
		ReviewID: "review-1", CommitID: "abc", Version: "1.0.0", Successful: false,
		FailureDetail: `failed to solve: failed to fetch oauth token: Post "https://ghcr.io/token": net/http: TLS handshake timeout`,
	}, pushNoticeDeps(t))
	if err == nil {
		t.Fatal("expected a failed build matching an infrastructure signature to be refused")
	}
	if !strings.Contains(err.Error(), "re-run the build") {
		t.Errorf("expected the refusal to name the remedy, got %v", err)
	}
	if calls.Load() != 0 {
		t.Errorf("expected the refusal before any platform call, got %d calls", calls.Load())
	}

	if err := ensureNotKnownInfrastructureBuildFailure(false, "make: *** [Makefile:1060: check] Error 2"); err != nil {
		t.Errorf("an ordinary red build must still be recordable as FAILED, got %v", err)
	}
	if err := ensureNotKnownInfrastructureBuildFailure(true, "tls handshake timeout"); err != nil {
		t.Errorf("a successful build is never refused, got %v", err)
	}
}

func TestBuildInconclusiveSignaturesReturnsACopy(t *testing.T) {
	signatures := BuildInconclusiveSignatures()
	if len(signatures) == 0 {
		t.Fatal("expected the classifier to carry signatures")
	}
	signatures[0] = "mutated"
	if BuildInconclusiveSignatures()[0] == "mutated" {
		t.Fatal("mutating the returned slice changed the classifier's own list")
	}
}
