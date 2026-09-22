package routes

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// pathIDRequest drives the wrapped handler for apiPath the way the mux does:
// the path values are already decoded and set on the request by the time a
// handler runs, so a test sets them directly.
func pathIDRequest(t *testing.T, apiPath string, handler http.Handler, values map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for name, value := range values {
		req.SetPathValue(name, value)
	}
	rec := httptest.NewRecorder()
	WithUUIDPathIDs(apiPath, handler).ServeHTTP(rec, req)
	return rec
}

// TestPathParamNamesReadsOnlyWholeSegments: the parser decides which segments
// are guarded, so it must not mistake a literal segment that merely contains
// braces -- or a segment that is not a parameter at all -- for one.
func TestPathParamNamesReadsOnlyWholeSegments(t *testing.T) {
	cases := []struct {
		apiPath string
		want    []string
	}{
		{"/v1/reviews", nil},
		{"/v1/reviews/{review_id}", []string{"review_id"}},
		{"/v1/reviews/{review_id}/builds/{build_id}", []string{"review_id", "build_id"}},
		{"/v1/reviews/merge-queue", nil},
		{"/v1/{plural}/x", []string{"plural"}},
		{"/v1/braces{not-a-param}", nil},
	}
	for _, c := range cases {
		got := pathParamNames(c.apiPath)
		if len(got) != len(c.want) {
			t.Fatalf("pathParamNames(%q) = %v, want %v", c.apiPath, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("pathParamNames(%q) = %v, want %v", c.apiPath, got, c.want)
			}
		}
	}
}

// TestWithUUIDPathIDsRefusesAMalformedIDBeforeTheHandlerRuns is the guard's
// own contract: a value that cannot be an API id is answered by the routes
// layer, so the handler never sees it and nothing reaches the database to be
// rejected there as a server fault.
func TestWithUUIDPathIDsRefusesAMalformedIDBeforeTheHandlerRuns(t *testing.T) {
	reached := false
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	for _, malformed := range []string{
		"not-a-uuid",
		"12345",
		"0",
		"gr_01HZZZZZZZZZZZZZZZZZZZZZZZ",
		"",
		// Shapes uuid.Parse accepts but this API never emits, and the
		// database reads differently from the canonical form.
		"01a01b39000070008000000000000000",
		"{01a01b39-0000-7000-8000-000000000000}",
		"urn:uuid:01a01b39-0000-7000-8000-000000000000",
	} {
		rec := pathIDRequest(t, "/v1/reviews/{review_id}", handler, map[string]string{"review_id": malformed})
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("id %q: status = %d, want 400: %s", malformed, rec.Code, rec.Body.String())
		}
		var body errorEnvelope
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("id %q: response body is not JSON: %v", malformed, err)
		}
		if body.Code != pathIDErrorCode {
			t.Fatalf("id %q: code = %q, want %q", malformed, body.Code, pathIDErrorCode)
		}
		if !strings.Contains(body.Message, "review_id") {
			t.Fatalf("id %q: message %q does not name the offending parameter", malformed, body.Message)
		}
	}
	if reached {
		t.Fatal("the handler ran for a malformed id; the guard must answer before it does")
	}
}

// TestWithUUIDPathIDsAcceptsEveryWellFormedID: only the spelling is judged.
// Requiring UUIDv7 here would be wrong -- a well-formed id that names nothing
// is a 404, not a malformed request, and the nil UUID is the canonical absent
// id callers exercise the not-found path with.
func TestWithUUIDPathIDsAcceptsEveryWellFormedID(t *testing.T) {
	for _, wellFormed := range []string{
		"01a01b39-0000-7000-8000-000000000000",
		"01A01B39-0000-7000-8000-000000000000",
		"00000000-0000-0000-0000-000000000000",
		"ffffffff-ffff-ffff-ffff-ffffffffffff",
	} {
		reached := false
		handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
		rec := pathIDRequest(t, "/v1/reviews/{review_id}", handler, map[string]string{"review_id": wellFormed})
		if rec.Code != http.StatusOK {
			t.Fatalf("id %q: status = %d, want the handler's own response: %s", wellFormed, rec.Code, rec.Body.String())
		}
		if !reached {
			t.Fatalf("id %q: the handler did not run", wellFormed)
		}
	}
}

// TestWithUUIDPathIDsLeavesNonIDParametersAlone: not every path parameter is
// an id. These two carry a name the caller chose and an identifier another
// system minted, so guarding them would refuse values that are correct.
func TestWithUUIDPathIDsLeavesNonIDParametersAlone(t *testing.T) {
	cases := []struct {
		apiPath string
		name    string
		value   string
	}{
		{"/v1/cloud-provider-aliases/{alias}", "alias", "my-aws-prod"},
		{"/v1/identity/users/{external_id}/deactivate", "external_id", "123456789012345678"},
	}
	for _, c := range cases {
		reached := false
		handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })
		rec := pathIDRequest(t, c.apiPath, handler, map[string]string{c.name: c.value})
		if rec.Code != http.StatusOK || !reached {
			t.Fatalf("%s: status = %d (handler ran: %v), want the handler to run", c.apiPath, rec.Code, reached)
		}
	}
}

// TestWithUUIDPathIDsChecksEveryIDParameter: a route with more than one id
// must not stop at the first well-formed one.
func TestWithUUIDPathIDsChecksEveryIDParameter(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	rec := pathIDRequest(t, "/v1/reviews/{review_id}/builds/{build_id}", handler, map[string]string{
		"review_id": "01a01b39-0000-7000-8000-000000000000",
		"build_id":  "not-a-uuid",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "build_id") {
		t.Fatalf("body %s does not name the malformed parameter", rec.Body.String())
	}
}

// TestWithUUIDPathIDsLeavesParameterlessRoutesUntouched: a pattern with no
// id parameter has nothing to guard, so the request reaches the handler
// unchanged.
func TestWithUUIDPathIDsLeavesParameterlessRoutesUntouched(t *testing.T) {
	reached := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusTeapot)
	})
	rec := pathIDRequest(t, "/v1/reviews", handler, map[string]string{"review_id": "not-a-uuid"})
	if !reached {
		t.Fatal("the handler did not run")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want the handler's own response", rec.Code)
	}
}
