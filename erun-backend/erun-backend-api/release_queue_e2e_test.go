package backendapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/model"
	"github.com/sophium/erun/erun-backend/erun-backend-api/internal/repository"
)

// The release queue no longer runs `erun release` itself: running a
// release is the environment's own job, the same shift the merge queue made.
// What the platform still owns is the queue's idempotency — recording a
// trigger exactly once per (tenant, commit) and never minting a second
// version for one merge commit — which is what this gate proves over the
// HTTP API against a real migrated Postgres. The claim/outcome/expiry SQL
// contracts a future environment-reported "release finished" path will need
// are exercised directly against Postgres in release_queue_sql_e2e_test.go;
// there is no Job, cluster, or DBOS workflow left here to be a gate on.
func releaseQueueE2EFromEnv(t *testing.T) string {
	t.Helper()
	databaseURL := os.Getenv("ERUN_E2E_RELEASE_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("opt-in: set ERUN_E2E_RELEASE_DATABASE_URL to a migrated PostgreSQL")
	}
	return databaseURL
}

func startReleaseQueueAPI(t *testing.T, databaseURL string) *httptest.Server {
	t.Helper()

	db, err := sql.Open("pgx", databaseURL)
	mustNoErr(t, err, "open db")
	t.Cleanup(func() { _ = db.Close() })

	handler, err := NewHandler(HandlerOptions{
		TokenVerifier: TokenVerifierFunc(func(_ context.Context, token string) (Claims, error) {
			if token != e2eDevToken {
				return Claims{}, errors.New("invalid dev token")
			}
			return Claims{Issuer: "https://dev.local", Subject: "dev-user", Username: "dev"}, nil
		}),
		IdentityCache: NewIdentityResolutionCache(IdentityCacheOptions{}),
		DB:            db,
		DBDialect:     repository.DialectPostgres,
	})
	mustNoErr(t, err, "new handler")

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

type releaseResponse struct {
	ReleaseID     string              `json:"releaseId"`
	Status        model.ReleaseStatus `json:"status"`
	Attempt       int                 `json:"attempt"`
	Version       string              `json:"version"`
	FailureReason string              `json:"failureReason"`
}

func e2eTriggerRelease(t *testing.T, baseURL, reviewID, targetBranch, commitID string, wantCode int) releaseResponse {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodPost, "/v1/releases", map[string]any{
		"reviewId":     reviewID,
		"targetBranch": targetBranch,
		"commitId":     commitID,
	})
	if code != wantCode {
		t.Fatalf("trigger release for %s: HTTP %d (want %d): %s", commitID, code, wantCode, body)
	}
	var release releaseResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &release), "parse release response")
	if release.ReleaseID == "" {
		t.Fatalf("trigger returned no release id: %s", body)
	}
	return release
}

func readRelease(t *testing.T, baseURL, releaseID string) releaseResponse {
	t.Helper()
	code, body := e2eRequest(t, baseURL, http.MethodGet, "/v1/releases/"+releaseID, nil)
	if code != http.StatusOK {
		t.Fatalf("get release %s: HTTP %d: %s", releaseID, code, body)
	}
	var release releaseResponse
	mustNoErr(t, json.Unmarshal([]byte(body), &release), "parse release response")
	return release
}

// TestReleaseQueueTriggerIsIdempotentPerCommitEndToEnd is the idempotency
// contract the trigger endpoint has to hold over HTTP: re-triggering an
// already-queued commit returns the same row, never a second one, and a
// caller can tell the two cases apart by status code.
func TestReleaseQueueTriggerIsIdempotentPerCommitEndToEnd(t *testing.T) {
	databaseURL := releaseQueueE2EFromEnv(t)
	srv := startReleaseQueueAPI(t, databaseURL)
	commit := fmt.Sprintf("%040x", time.Now().UnixNano())

	first := e2eTriggerRelease(t, srv.URL, "", "main", commit, http.StatusCreated)
	if first.Status != model.ReleaseStatusQueued {
		t.Fatalf("status = %q, want queued", first.Status)
	}

	again := e2eTriggerRelease(t, srv.URL, "", "main", commit, http.StatusOK)
	if again.ReleaseID != first.ReleaseID {
		t.Fatalf("re-trigger created release %s, want the same row %s", again.ReleaseID, first.ReleaseID)
	}

	fromGet := readRelease(t, srv.URL, first.ReleaseID)
	if fromGet.ReleaseID != first.ReleaseID {
		t.Fatalf("GET returned %s, want %s", fromGet.ReleaseID, first.ReleaseID)
	}
}

// TestReleaseQueueRejectsAnInvalidTriggerEndToEnd: a trigger with no commit or
// no target branch is refused before anything is queued.
func TestReleaseQueueRejectsAnInvalidTriggerEndToEnd(t *testing.T) {
	databaseURL := releaseQueueE2EFromEnv(t)
	srv := startReleaseQueueAPI(t, databaseURL)

	code, body := e2eRequest(t, srv.URL, http.MethodPost, "/v1/releases", map[string]any{"targetBranch": "main"})
	if code != http.StatusBadRequest {
		t.Fatalf("trigger with no commitId: HTTP %d (want 400): %s", code, body)
	}
}
