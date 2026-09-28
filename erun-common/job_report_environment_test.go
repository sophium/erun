package eruncommon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// environmentJobReportPlatform is the reporting contract's own hermetic
// wiring, reused from push_review_notice_test.go: one erun-type alias pointed
// at a local server, authenticating with an already-cached access token so
// nothing is minted and no host outside this process is reached. Every test
// below drives the real reporting path end to end against it.
func environmentJobReportPlatform(t *testing.T, apiURL string) (CloudReadStore, CloudDependencies) {
	t.Helper()
	return pushNoticeStoreWithAlias(apiURL), pushNoticeDeps(t)
}

// environmentJobPlatformRequests is every request the fake platform received,
// in order, so a test can assert on what was sent and on when.
type environmentJobPlatformRequests struct {
	mu       sync.Mutex
	requests []environmentJobPlatformRequest
}

type environmentJobPlatformRequest struct {
	method string
	path   string
	body   map[string]any
}

func (r *environmentJobPlatformRequests) record(t *testing.T, req *http.Request) {
	t.Helper()
	var body map[string]any
	if req.Body != nil {
		_ = json.NewDecoder(req.Body).Decode(&body)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, environmentJobPlatformRequest{method: req.Method, path: req.URL.Path, body: body})
}

// find returns the first request matching method and path, and whether there
// was one.
func (r *environmentJobPlatformRequests) find(method, path string) (environmentJobPlatformRequest, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, recorded := range r.requests {
		if recorded.method == method && recorded.path == path {
			return recorded, true
		}
	}
	return environmentJobPlatformRequest{}, false
}

func (r *environmentJobPlatformRequests) all() []environmentJobPlatformRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]environmentJobPlatformRequest(nil), r.requests...)
}

// lastBody is the body of the request being answered, which the fake needs
// when its response is meant to echo what was asked for.
func (r *environmentJobPlatformRequests) lastBody(t *testing.T) map[string]any {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.requests) == 0 {
		return nil
	}
	return r.requests[len(r.requests)-1].body
}

// environmentJobPlatformServer is the platform this test's environment talks
// to: it resolves the environment by name, accepts the job claim, and accepts
// the outcome update. startedMarker names a file the work creates, and the
// claim handler refuses to answer if it already exists -- the queue's whole
// value is that a row exists *before* the work runs, so a claim arriving after
// the work started is a failure of the ordering this exists to guarantee.
func environmentJobPlatformServer(t *testing.T, environment, startedMarker string) (*httptest.Server, *environmentJobPlatformRequests) {
	t.Helper()
	requests := &environmentJobPlatformRequests{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.record(t, r)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/environments":
			_ = json.NewEncoder(w).Encode([]PlatformEnvironment{{EnvironmentID: "env-1", Name: environment}})
		case r.Method == http.MethodPost && r.URL.Path == "/v1/jobs":
			if _, err := os.Stat(startedMarker); err == nil {
				t.Errorf("the platform job was claimed after the work had already started; a queue that shows work only once it is done cannot be read as work in flight")
			}
			_ = json.NewEncoder(w).Encode(PlatformJob{JobID: "job-platform-1", Status: JobStatusRunning})
		case r.Method == http.MethodPatch && r.URL.Path == "/v1/jobs/job-platform-1":
			status, _ := requests.lastBody(t)["status"].(string)
			_ = json.NewEncoder(w).Encode(PlatformJob{JobID: "job-platform-1", Status: status})
		default:
			t.Errorf("unexpected platform request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, requests
}

// TestRunEnvironmentJobSupervisorRecordsTheJobOnThePlatformQueue is the
// reproduction of the gap this wires up: work started through erun's own
// surface recorded nothing on the platform queue, because nothing called
// ReportJobStart/ReportJobOutcome, so the queue held only what an actor
// entered by hand with `erun jobs start`. RunEnvironmentJobSupervisor is the
// process `erun exec job start` detaches (and the one exec_raw/exec_agent
// reach), so driving it is driving that surface.
//
// It asserts both halves: the row is opened before the work runs, and closed
// with the outcome the supervisor observed. Before this change the fake
// platform received no request at all.
func TestRunEnvironmentJobSupervisorRecordsTheJobOnThePlatformQueue(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(environmentJobIDEnvVar, "")
	tenant, environment := "acme", "dev"
	startedMarker := filepath.Join(t.TempDir(), "work-started")
	srv, requests := environmentJobPlatformServer(t, environment, startedMarker)
	store, deps := environmentJobReportPlatform(t, srv.URL)

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          "gate-run",
		Name:        "gate the prospective merge",
		Command:     []string{"sh", "-c", fmt.Sprintf("printf x > %s; exit 0", startedMarker)},
		Store:       store,
		Deps:        deps,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	claimed, ok := requests.find(http.MethodPost, "/v1/jobs")
	if !ok {
		t.Fatalf("no job was claimed on the platform queue; requests were %+v", requests.all())
	}
	// The claim carries what the queue groups and coordinates by: the job's
	// own kind, the prose describing it, the pod it runs in, and the local
	// job record it mirrors.
	for field, want := range map[string]string{
		"jobType":       JobTypeMaintenance,
		"summary":       "gate the prospective merge",
		"actorKind":     environmentJobPlatformActorKind,
		"actorId":       "acme/dev",
		"environmentId": "env-1",
		"localJobId":    "gate-run",
	} {
		if got, _ := claimed.body[field].(string); got != want {
			t.Errorf("claim %s = %q, want %q", field, got, want)
		}
	}

	closed, ok := requests.find(http.MethodPatch, "/v1/jobs/job-platform-1")
	if !ok {
		t.Fatalf("the claimed job was never closed; requests were %+v", requests.all())
	}
	if got, _ := closed.body["status"].(string); got != JobStatusSucceeded {
		t.Errorf("outcome status = %q, want %q", got, JobStatusSucceeded)
	}

	// The pod's own record names the row it opened, so a reader can tie what
	// the environment reports to what the queue shows.
	finished, err := LoadEnvironmentJob(tenant, environment, "gate-run", time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if finished.PlatformJobID != "job-platform-1" {
		t.Errorf("PlatformJobID = %q, want the claimed row's id", finished.PlatformJobID)
	}
}

// TestRunEnvironmentJobSupervisorReportsAFailedJobAsFailed is the outcome half
// of the same contract: a job that ends non-zero must reach the queue as a
// failure, not stay RUNNING until the platform's own sweep gives up on it.
func TestRunEnvironmentJobSupervisorReportsAFailedJobAsFailed(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(environmentJobIDEnvVar, "")
	tenant, environment := "acme", "dev"
	srv, requests := environmentJobPlatformServer(t, environment, filepath.Join(t.TempDir(), "unused-marker"))
	store, deps := environmentJobReportPlatform(t, srv.URL)

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          "failing-run",
		Name:        "a run that fails",
		Command:     []string{"sh", "-c", "exit 3"},
		Store:       store,
		Deps:        deps,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	closed, ok := requests.find(http.MethodPatch, "/v1/jobs/job-platform-1")
	if !ok {
		t.Fatalf("the claimed job was never closed; requests were %+v", requests.all())
	}
	if got, _ := closed.body["status"].(string); got != JobStatusFailed {
		t.Errorf("outcome status = %q, want %q", got, JobStatusFailed)
	}
}

// TestRunEnvironmentJobSupervisorAttributesANestedJobToItsParent: the queue's
// actor is who holds the job, and work started from inside another job's own
// work belongs to that job rather than to the environment it happens to share
// with everything else running there. It is the same fact
// EnvironmentJob.StartedByJobID records, carried onto the platform row so a
// reader can see the nesting without walking the pod's own records.
func TestRunEnvironmentJobSupervisorAttributesANestedJobToItsParent(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(environmentJobIDEnvVar, "")
	tenant, environment := "acme", "dev"
	srv, requests := environmentJobPlatformServer(t, environment, filepath.Join(t.TempDir(), "unused-marker"))
	store, deps := environmentJobReportPlatform(t, srv.URL)

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:         tenant,
		Environment:    environment,
		ID:             "nested-gate",
		Name:           "a gate run from inside an agent's own work",
		Command:        []string{"sh", "-c", "exit 0"},
		StartedByJobID: "parent-agent-run",
		Store:          store,
		Deps:           deps,
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	claimed, ok := requests.find(http.MethodPost, "/v1/jobs")
	if !ok {
		t.Fatalf("no job was claimed on the platform queue; requests were %+v", requests.all())
	}
	if got, _ := claimed.body["actorId"].(string); got != "parent-agent-run" {
		t.Errorf("claim actorId = %q, want the job this one was started from", got)
	}
}

// TestRunEnvironmentJobSupervisorWithoutAPlatformAliasStaysOffTheNetwork pins
// the half of the contract that protects every environment that has no
// platform: the supervisor of an unconfigured install must reach no host and
// leave no platform job id behind. Reporting is best-effort, so "nothing
// configured" is complete silence rather than a skip notice.
func TestRunEnvironmentJobSupervisorWithoutAPlatformAliasStaysOffTheNetwork(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv(environmentJobIDEnvVar, "")
	tenant, environment := "acme", "dev"
	reached := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case reached <- r.Method + " " + r.URL.Path:
		default:
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := RunEnvironmentJobSupervisor(EnvironmentJobSupervisorParams{
		Tenant:      tenant,
		Environment: environment,
		ID:          "quiet-run",
		Name:        "work in an environment with no platform",
		Command:     []string{"sh", "-c", "exit 0"},
		Store:       jobReportStore{},
		Deps:        CloudDependencies{},
	}); err != nil {
		t.Fatalf("RunEnvironmentJobSupervisor: %v", err)
	}

	select {
	case request := <-reached:
		t.Fatalf("an install with no platform alias reached %s; the contract is no network call at all", request)
	default:
	}

	finished, err := LoadEnvironmentJob(tenant, environment, "quiet-run", time.Now())
	if err != nil {
		t.Fatalf("LoadEnvironmentJob: %v", err)
	}
	if finished.PlatformJobID != "" {
		t.Errorf("PlatformJobID = %q, want empty with nothing to record to", finished.PlatformJobID)
	}
	if !finished.Succeeded {
		t.Fatalf("the job itself = %+v, want it to have succeeded unaffected by reporting", finished)
	}
}
