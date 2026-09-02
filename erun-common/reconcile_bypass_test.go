package eruncommon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The wire shape of the GitHub rule-suites requests (list, pagination via
// Link header, per-suite detail) has no integration-suite equivalent, the
// same reasoning report_commit_status_test.go documents: validation,
// dry-run, remote-url parsing, and the no-token refusal are exercised
// end-to-end from the binary in erun-integration/exec_test.go instead.

func TestListBypassedRuleSuitesPaginatesViaLinkHeader(t *testing.T) {
	var gotPaths []string
	var serverURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.RequestURI())
		if r.Header.Get("Authorization") != "Bearer gho_test_token" {
			t.Errorf("unexpected auth header: %q", r.Header.Get("Authorization"))
		}
		if strings.Contains(r.URL.RawQuery, "page=2") {
			_, _ = w.Write([]byte(`[{"id":2,"actor_name":"bot","after_sha":"sha2","pushed_at":"2026-09-02T00:00:00Z","result":"bypass"}]`))
			return
		}
		// serverURL is set once NewServer below returns, before any request
		// is actually sent, so this closure sees a real value at call time.
		w.Header().Set("Link", fmt.Sprintf(`<%s/repos/o/r/rulesets/rule-suites?page=2>; rel="next"`, serverURL))
		_, _ = w.Write([]byte(`[{"id":1,"actor_name":"bot","after_sha":"sha1","pushed_at":"2026-09-01T00:00:00Z","result":"bypass"}]`))
	}))
	defer server.Close()
	serverURL = server.URL

	suites, truncated, err := listBypassedRuleSuites(context.Background(), server.Client(), "gho_test_token", server.URL+"/repos/o/r/rulesets/rule-suites")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if truncated {
		t.Fatalf("expected pagination to exhaust, not truncate")
	}
	if len(suites) != 2 || suites[0].ID != 1 || suites[1].ID != 2 {
		t.Fatalf("unexpected suites: %+v", suites)
	}
	if len(gotPaths) != 2 {
		t.Fatalf("expected 2 requests, got %d: %v", len(gotPaths), gotPaths)
	}
}

func TestListBypassedRuleSuitesSurfacesAGitHubFailureResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
	}))
	defer server.Close()

	_, _, err := listBypassedRuleSuites(context.Background(), server.Client(), "gho_test_token", server.URL+"/repos/o/r/rulesets/rule-suites")
	if err == nil {
		t.Fatal("expected a github failure response to surface as an error")
	}
	if !strings.Contains(err.Error(), "Resource not accessible") {
		t.Fatalf("expected github's own message in the error, got: %v", err)
	}
}

func TestGetGitHubRuleSuiteDetailReturnsRuleEvaluations(t *testing.T) {
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"rule_evaluations":[
			{"rule_source":{"type":"ruleset","id":11081432},"result":"fail","rule_type":"pull_request"},
			{"rule_source":{"type":"ruleset","id":11081432},"result":"pass","rule_type":"non_fast_forward"}
		]}`))
	}))
	defer server.Close()
	restoreBaseURL := githubAPIBaseURL
	githubAPIBaseURL = server.URL + "/"
	defer func() { githubAPIBaseURL = restoreBaseURL }()

	detail, err := getGitHubRuleSuiteDetail(context.Background(), server.Client(), "gho_test_token", "sophium", "erun", 3916885473)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/repos/sophium/erun/rulesets/rule-suites/3916885473" {
		t.Fatalf("unexpected path: %s", gotPath)
	}
	if len(detail.RuleEvaluations) != 2 {
		t.Fatalf("unexpected rule evaluations: %+v", detail.RuleEvaluations)
	}
}

func TestBypassedRuleForRulesetOnlyMatchesTheNamedRulesetsFailure(t *testing.T) {
	evaluations := []githubRuleEvaluation{
		{RuleSource: struct {
			Type string `json:"type"`
			ID   int64  `json:"id"`
		}{Type: "ruleset", ID: 99}, Result: "fail", RuleType: "deletion"},
		{RuleSource: struct {
			Type string `json:"type"`
			ID   int64  `json:"id"`
		}{Type: "ruleset", ID: 11081432}, Result: "fail", RuleType: "pull_request"},
		{RuleSource: struct {
			Type string `json:"type"`
			ID   int64  `json:"id"`
		}{Type: "ruleset", ID: 11081432}, Result: "pass", RuleType: "non_fast_forward"},
	}
	rule, ok := bypassedRuleForRuleset(evaluations, 11081432)
	if !ok || rule != "pull_request" {
		t.Fatalf("expected pull_request bypassed for ruleset 11081432, got %q, ok=%v", rule, ok)
	}
	if _, ok := bypassedRuleForRuleset(evaluations, 424242); ok {
		t.Fatalf("expected no match for an unrelated ruleset id")
	}
}

func TestReconcileAgainstGateRunsMarksExactCommitMatchesOnly(t *testing.T) {
	pushes := []BypassedPush{
		{Commit: "sha1"},
		{Commit: "sha2"},
	}
	passed := []PlatformGateRun{
		{GateRunID: "gr_1", MergeCommit: "sha1"},
		{GateRunID: "gr_2", MergeCommit: "shaX"},
	}
	reconcileAgainstGateRuns(pushes, passed)
	if !pushes[0].Reconciled || pushes[0].GateRunID != "gr_1" {
		t.Fatalf("expected sha1 reconciled against gr_1, got %+v", pushes[0])
	}
	if pushes[1].Reconciled {
		t.Fatalf("expected sha2 to stay unreconciled, got %+v", pushes[1])
	}
}

func TestResolveReconcileBypassInputsRequiresTargetBranchAndRulesetID(t *testing.T) {
	if _, err := resolveReconcileBypassInputs(Context{}, ReconcileBypassParams{
		RemoteURL: "git@github.com:sophium/erun.git", RulesetID: 11081432,
	}); err == nil || !strings.Contains(err.Error(), "target branch is required") {
		t.Fatalf("expected a target branch requirement error, got: %v", err)
	}
	if _, err := resolveReconcileBypassInputs(Context{}, ReconcileBypassParams{
		RemoteURL: "git@github.com:sophium/erun.git", TargetBranch: "main",
	}); err == nil || !strings.Contains(err.Error(), "ruleset id is required") {
		t.Fatalf("expected a ruleset id requirement error, got: %v", err)
	}
	inputs, err := resolveReconcileBypassInputs(Context{}, ReconcileBypassParams{
		RemoteURL: "git@github.com:sophium/erun.git", RulesetID: 11081432, TargetBranch: "main",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if inputs.owner != "sophium" || inputs.repo != "erun" || inputs.rulesetID != 11081432 || inputs.targetBranch != "main" {
		t.Fatalf("unexpected inputs: %+v", inputs)
	}
}

func TestGithubRuleSuitesURLEncodesRefAndOptionalSince(t *testing.T) {
	url := githubRuleSuitesURL("sophium", "erun", "main", "")
	if want := githubAPIBaseURL + "repos/sophium/erun/rulesets/rule-suites?per_page=100&ref=refs%2Fheads%2Fmain&rule_suite_result=bypass"; url != want {
		t.Fatalf("got %q, want %q", url, want)
	}
	url = githubRuleSuitesURL("sophium", "erun", "main", "week")
	if want := githubAPIBaseURL + "repos/sophium/erun/rulesets/rule-suites?per_page=100&ref=refs%2Fheads%2Fmain&rule_suite_result=bypass&time_period=week"; url != want {
		t.Fatalf("got %q, want %q", url, want)
	}
}
