package backendapi

import (
	"net/http"
	"strings"
	"testing"
)

// malformedPathIDCases is every route shape that takes an externally visible
// id, driven with a path segment that is not one. The list is deliberately
// wider than the two routes the reports named: the defect was never
// route-specific, and the routes where the id is a list filter rather than a
// primary-key lookup -- /v1/reviews/{id}/builds, /comments, /releases,
// /reviewers, and /v1/users/{id}/roles -- are the ones that still reached the
// database and came back as a server fault once the by-id lookups had been
// fixed.
var malformedPathIDCases = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/v1/reviews/not-a-uuid"},
	{http.MethodGet, "/v1/gate-runs/not-a-uuid"},
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
	{http.MethodPatch, "/v1/gate-runs/not-a-uuid"},
	{http.MethodPatch, "/v1/jobs/not-a-uuid"},
	{http.MethodPost, "/v1/invite-requests/not-a-uuid/approve"},
}

// absentPathIDs is the control both reports used: a well-formed id that names
// nothing. It proves the handler and the not-found path both work, so a
// failure above is isolated to parsing rather than to lookup.
var absentPathIDs = []string{
	"/v1/reviews/00000000-0000-0000-0000-000000000000",
	"/v1/gate-runs/00000000-0000-0000-0000-000000000000",
	"/v1/environments/00000000-0000-0000-0000-000000000000",
	"/v1/contexts/00000000-0000-0000-0000-000000000000",
}

// TestMalformedPathIDInAnIDFilterIsNotAServerFault is the reproduction of the
// reported failure: a path segment that is not an id still reached the
// database, which rejected it at parse time with SQLSTATE 22P02, and that
// driver error matched none of the repository's sentinels -- so the route
// layer's generic fallback answered 500 INTERNAL_SERVER_ERROR for a mistyped
// id, sending the reader to check the control plane's health for their own
// typo and putting every such typo into the platform's 5xx rate.
//
// These are the routes where the id is a list filter rather than a
// primary-key lookup, which is the half the by-id lookup fix never covered.
// On the code before the routes layer's guard each one answers 500; the
// assertion below is on the status class first, so the failure this test
// reports is the one the reports described.
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

	// The control the reports used: a well-formed id that names nothing is
	// absent, not malformed, and keeps answering 404 on the same route family.
	code, body := e2eRequest(t, srv.URL, http.MethodGet, "/v1/reviews/00000000-0000-0000-0000-000000000000/builds", nil)
	if code != http.StatusOK {
		t.Fatalf("well-formed absent id: HTTP %d (want 200, an empty build list): %s", code, body)
	}
}

// TestMalformedPathIDIsAClientErrorOnEveryIDRoute is the same guard's
// contract across every route shape that takes an id. It asserts the machine
// code as well as the status, because "which id was wrong, and in what way"
// is the part a caller can act on, and it keeps the two situations a client
// branches on distinct: a malformed id is the caller's to fix, an absent one
// is a 404.
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

	// The control: only the spelling is judged. A well-formed id that names
	// nothing must keep answering 404 -- collapsing it into the 400 above
	// would tell a caller to fix an id that is already correct.
	for _, path := range absentPathIDs {
		code, body := e2eRequest(t, srv.URL, http.MethodGet, path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("well-formed absent id %s: HTTP %d (want 404): %s", path, code, body)
		}
	}
}

// TestMalformedPathIDIsStillRejectedAsUnauthenticated: the guard sits inside
// the authentication wrapper, so an anonymous caller gets 401 for every id
// shape. Whether a given id parses must not be observable before the caller
// is authorized.
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
