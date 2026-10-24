package integration

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// jobListStubServer answers `erun jobs list`'s own read with the supplied
// jobs, so a real-run scenario exercises the listing and rendering path end to
// end rather than only the --dry-run trace branch. A nil slice is a real
// empty queue, not a missing route.
func jobListStubServer(t testing.TB, jobs []map[string]any) *httptest.Server {
	t.Helper()
	if jobs == nil {
		jobs = []map[string]any{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(jobs)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

// platformJobJSON is one job's wire shape, with every timestamp fixed so a
// golden records the contract rather than the clock.
func platformJobJSON(overrides map[string]any) map[string]any {
	job := map[string]any{
		"jobId":         "job_01HQ",
		"jobType":       "fix",
		"summary":       "fixing the jobs claim race",
		"status":        "RUNNING",
		"actorKind":     "agent",
		"actorId":       "erun/code2",
		"startedAt":     "2026-01-02T03:04:05Z",
		"createdAt":     "2026-01-02T03:04:05Z",
		"updatedAt":     "2026-01-02T03:04:05Z",
		"environmentId": "",
	}
	for key, value := range overrides {
		job[key] = value
	}
	return job
}

// jobsAPIServer is the jobs-API double `erun jobs` drives for real runs: the
// queue read, the single-job read, the claim, and the update. Each handler
// proves the CLI attached the bearer it minted. The claim route answers 409
// with the JOB_SCOPE_HELD envelope when the request carries a scope the
// scenario has declared held, so a refused claim is exercised as the
// structured refusal it is rather than as an opaque conflict.
func jobsAPIServer(t testing.TB, heldScope string, environments []map[string]any) *httptest.Server {
	t.Helper()
	if environments == nil {
		environments = []map[string]any{}
	}
	mux := http.NewServeMux()

	mux.HandleFunc("GET /v1/environments", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(environments)
	})

	mux.HandleFunc("GET /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})

	mux.HandleFunc("POST /v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		var body struct {
			Scope     string `json:"scope"`
			JobType   string `json:"jobType"`
			ActorID   string `json:"actorId"`
			ActorKind string `json:"actorKind"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if heldScope != "" && body.Scope == heldScope {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusConflict)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    "JOB_SCOPE_HELD",
				"message": "scope is already claimed",
				"details": map[string]any{
					"scope":     heldScope,
					"jobId":     "job_01HQ_held",
					"actorId":   "erun/code4",
					"summary":   "running the gate for the same change",
					"startedAt": "2026-01-02T00:00:00Z",
				},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(platformJobJSON(map[string]any{"scope": body.Scope}))
	})

	mux.HandleFunc("GET /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(platformJobJSON(map[string]any{
			"jobId": r.PathValue("id"),
			"scope": "sophium/erun#1",
		}))
	})

	mux.HandleFunc("PATCH /v1/jobs/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !requireBearer(w, r) {
			return
		}
		_ = json.NewEncoder(w).Encode(platformJobJSON(map[string]any{
			"jobId":  r.PathValue("id"),
			"status": "SUCCEEDED",
		}))
	})

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}
