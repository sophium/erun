package review

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/infra/registry"
)

// honourFilters false models a platform that ignores ?commit= and returns its
// newest build.
type reconcileBypassStub struct {
	successfulBuilds map[string]string
	honourFilters    bool
	queries          []string
}

func (s *reconcileBypassStub) server(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/builds", func(w http.ResponseWriter, r *http.Request) {
		s.queries = append(s.queries, r.URL.RawQuery)
		page := platformclient.PlatformBuildPage{Builds: []platformclient.PlatformBuild{}}
		commit := r.URL.Query().Get("commit")
		switch {
		case !s.honourFilters:
			page.Builds = append(page.Builds, platformclient.PlatformBuild{BuildID: "newest", CommitID: "something-else", Successful: true})
		case s.successfulBuilds[commit] != "" && r.URL.Query().Get("successful") == "true":
			page.Builds = append(page.Builds, platformclient.PlatformBuild{BuildID: s.successfulBuilds[commit], CommitID: commit, Successful: true})
		}
		_ = json.NewEncoder(w).Encode(page)
	})
	mux.HandleFunc("GET /repos/sophium/erun/compare/{range}", func(w http.ResponseWriter, r *http.Request) {
		commits := map[string][]string{
			"before-release...stamp-tip":   {"release-commit", "checksum-sync", "stamp-tip"},
			"before-ungated...ungated-tip": {"ungated-tip"},
		}[r.PathValue("range")]
		payload := map[string][]map[string]string{"commits": {}}
		for _, sha := range commits {
			payload["commits"] = append(payload["commits"], map[string]string{"sha": sha})
		}
		_ = json.NewEncoder(w).Encode(payload)
	})
	mux.HandleFunc("GET /repos/sophium/erun/tags", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[{"name":"v1.0.0","commit":{"sha":"release-commit"}}]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	restore := registry.GithubAPIBaseURL
	registry.GithubAPIBaseURL = srv.URL + "/"
	t.Cleanup(func() { registry.GithubAPIBaseURL = restore })
	return srv
}

func reconcileBypassTestPushes() []BypassedPush {
	return []BypassedPush{
		{RuleSuiteID: 1, Actor: "erun-merge-queue", Commit: "merge-tip", BeforeCommit: "before-merge"},
		{RuleSuiteID: 2, Actor: "erun-merge-queue", Commit: "stamp-tip", BeforeCommit: "before-release"},
		{RuleSuiteID: 3, Actor: "a-human", Commit: "ungated-tip", BeforeCommit: "before-ungated"},
	}
}

func runAccountForBypassedPushes(t *testing.T, stub *reconcileBypassStub, pushes []BypassedPush) (int, int, error) {
	t.Helper()
	srv := stub.server(t)
	client := platformclient.NewPlatformClient(srv.URL, func() (string, error) { return "token", nil })
	inputs := reconcileBypassInputs{owner: "sophium", repo: "erun", targetBranch: "main", rulesetID: 1}
	return accountForBypassedPushes(context.Background(), srv.Client(), "gho_token", inputs, pushes, newSuccessfulBuildIndex(context.Background(), client))
}

func TestReconcileBypassAsksThePlatformOncePerCommit(t *testing.T) {
	stub := &reconcileBypassStub{honourFilters: true}
	pushes := []BypassedPush{
		{RuleSuiteID: 3, Actor: "a", Commit: "ungated-tip", BeforeCommit: "before-ungated"},
		{RuleSuiteID: 4, Actor: "a", Commit: "ungated-tip", BeforeCommit: "before-ungated"},
	}
	if _, _, err := runAccountForBypassedPushes(t, stub, pushes); err != nil {
		t.Fatalf("accountForBypassedPushes: %v", err)
	}
	if len(stub.queries) != 1 {
		t.Fatalf("builds queries = %v, want one per distinct commit", stub.queries)
	}
}

func TestReconcileBypassIgnoresABuildOfAnotherCommit(t *testing.T) {
	stub := &reconcileBypassStub{honourFilters: false}
	pushes := reconcileBypassTestPushes()[2:]
	unreconciled, _, err := runAccountForBypassedPushes(t, stub, pushes)
	if err != nil {
		t.Fatalf("accountForBypassedPushes: %v", err)
	}
	if unreconciled != 1 || pushes[0].BuildID != "" {
		t.Fatalf("push = %+v, want it left UNRECONCILED", pushes[0])
	}
}

func TestReconcileBypassFailsWhenTheBuildHistoryIsUnreadable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	client := platformclient.NewPlatformClient(srv.URL, func() (string, error) { return "token", nil })
	inputs := reconcileBypassInputs{owner: "sophium", repo: "erun", targetBranch: "main", rulesetID: 1}
	_, _, err := accountForBypassedPushes(context.Background(), srv.Client(), "gho_token", inputs, reconcileBypassTestPushes(), newSuccessfulBuildIndex(context.Background(), client))
	if err == nil || !strings.Contains(err.Error(), "list successful builds of merge-tip") {
		t.Fatalf("err = %v, want the unreadable build history named", err)
	}
}
