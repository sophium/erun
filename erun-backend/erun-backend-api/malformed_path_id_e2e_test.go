package backendapi

import (
	"net/http"
	"strings"
	"testing"
)

// Routes where the id is a list filter, not a primary-key lookup, are included:
// they are the ones that reached the database.
var malformedPathIDCases = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/v1/reviews/not-a-uuid"},
	{http.MethodGet, "/v1/environments/not-a-uuid"},
	{http.MethodGet, "/v1/contexts/not-a-uuid"},
	{http.MethodGet, "/v1/jobs/not-a-uuid"},
	{http.MethodGet, "/v1/releases/not-a-uuid"},
	{http.MethodGet, "/v1/reviews/not-a-uuid/builds"},
	{http.MethodGet, "/v1/reviews/not-a-uuid/builds/not-a-uuid"},
	{http.MethodGet, "/v1/reviews/not-a-uuid/comments"},
	{http.MethodGet, "/v1/reviews/not-a-uuid/releases"},
	{http.MethodGet, "/v1/reviews/not-a-uuid/reviewers"},
	{http.MethodGet, "/v1/users/not-a-uuid/roles"},
	{http.MethodGet, "/v1/environments/not-a-uuid/jobs"},
	{http.MethodPatch, "/v1/reviews/not-a-uuid/status"},
	{http.MethodPatch, "/v1/jobs/not-a-uuid"},
	{http.MethodPost, "/v1/invite-requests/not-a-uuid/approve"},
}

var absentPathIDs = []string{
	"/v1/reviews/00000000-0000-0000-0000-000000000000",
	"/v1/environments/00000000-0000-0000-0000-000000000000",
	"/v1/contexts/00000000-0000-0000-0000-000000000000",
}

// TestMalformedPathIDInAnIDFilterIsNotAServerFault: a malformed id must be refused before the
// database, which would answer SQLSTATE 22P02 (a 500).
func TestMalformedPathIDInAnIDFilterIsNotAServerFault(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	for _, path := range []string{
		"/v1/reviews/not-a-uuid/builds",
		"/v1/reviews/not-a-uuid/comments",
		"/v1/reviews/not-a-uuid/releases",
		"/v1/reviews/not-a-uuid/reviewers",
		"/v1/users/not-a-uuid/roles",
	} {
		code, body := e2eRequest(t, srv.URL, http.MethodGet, path, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("GET %s: HTTP %d (want 400, not a server fault): %s", path, code, body)
		}
		if strings.Contains(body, "INTERNAL_SERVER_ERROR") {
			t.Fatalf("GET %s: body %s still reports a server fault", path, body)
		}
	}

	code, body := e2eRequest(t, srv.URL, http.MethodGet, "/v1/reviews/00000000-0000-0000-0000-000000000000/builds", nil)
	if code != http.StatusOK {
		t.Fatalf("well-formed absent id: HTTP %d (want 200, an empty build list): %s", code, body)
	}
}

func TestMalformedPathIDIsAClientErrorOnEveryIDRoute(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	for _, c := range malformedPathIDCases {
		code, body := e2eRequest(t, srv.URL, c.method, c.path, nil)
		if code != http.StatusBadRequest {
			t.Fatalf("%s %s: HTTP %d (want 400): %s", c.method, c.path, code, body)
		}
		if !strings.Contains(body, "INVALID_PATH_ID") {
			t.Fatalf("%s %s: body %s does not carry the INVALID_PATH_ID code", c.method, c.path, body)
		}
	}

	// A well-formed absent id must stay 404, not 400.
	for _, path := range absentPathIDs {
		code, body := e2eRequest(t, srv.URL, http.MethodGet, path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("well-formed absent id %s: HTTP %d (want 404): %s", path, code, body)
		}
	}
}

// TestMalformedPathIDIsStillRejectedAsUnauthenticated: the guard sits inside authentication, so id
// parsing is not observable before authorization.
func TestMalformedPathIDIsStillRejectedAsUnauthenticated(t *testing.T) {
	config := mergeQueueE2EFromEnv(t)
	srv := startMergeQueueAPI(t, config)

	for _, c := range malformedPathIDCases {
		req, err := http.NewRequest(c.method, srv.URL+c.path, nil)
		mustNoErr(t, err, "new request")
		resp, err := http.DefaultClient.Do(req)
		mustNoErr(t, err, "do request")
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("%s %s unauthenticated: HTTP %d (want 401)", c.method, c.path, resp.StatusCode)
		}
	}
}
