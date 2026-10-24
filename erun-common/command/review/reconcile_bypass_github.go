package review

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/sophium/erun/erun-common/infra/registry"
)

// githubRuleSuiteSummary is one entry from GitHub's rule-suites list.
type githubRuleSuiteSummary struct {
	ID        int64  `json:"id"`
	ActorName string `json:"actor_name"`
	BeforeSHA string `json:"before_sha"`
	AfterSHA  string `json:"after_sha"`
	PushedAt  string `json:"pushed_at"`
	Result    string `json:"result"`
}

type githubRuleEvaluation struct {
	RuleSource struct {
		Type string `json:"type"`
		ID   int64  `json:"id"`
	} `json:"rule_source"`
	Result   string `json:"result"`
	RuleType string `json:"rule_type"`
}

type githubRuleSuiteDetail struct {
	RuleEvaluations []githubRuleEvaluation `json:"rule_evaluations"`
}

// githubRuleSuitesURL builds the bypassed-rule-suites lookup URL for owner/
// repo/targetBranch, optionally narrowed by since (rule-suites' own
// time_period values: hour, day, week, month).
func githubRuleSuitesURL(owner, repo, targetBranch, since string) string {
	values := url.Values{
		"ref":               {"refs/heads/" + targetBranch},
		"rule_suite_result": {"bypass"},
		"per_page":          {"100"},
	}
	if since != "" {
		values.Set("time_period", since)
	}
	return fmt.Sprintf("%srepos/%s/%s/rulesets/rule-suites?%s", registry.GithubAPIBaseURL, owner, repo, values.Encode())
}

// listBypassedRuleSuites walks GitHub's rule-suites pagination (via the
// response's Link header) up to maxRuleSuitePages, returning every bypassed
// suite it read and whether it stopped early because more pages remained.
func listBypassedRuleSuites(ctx context.Context, client *http.Client, token, firstURL string) ([]githubRuleSuiteSummary, bool, error) {
	var all []githubRuleSuiteSummary
	next := firstURL
	for page := 0; next != "" && page < maxRuleSuitePages; page++ {
		var batch []githubRuleSuiteSummary
		link, err := getGitHubJSONPage(ctx, client, token, next, &batch)
		if err != nil {
			return nil, false, err
		}
		all = append(all, batch...)
		next = link
	}
	return all, next != "", nil
}

// githubNextPageLink extracts the rel="next" URL from a GitHub Link header,
// or "" when there is no next page.
func githubNextPageLink(linkHeader string) string {
	for _, part := range strings.Split(linkHeader, ",") {
		segments := strings.Split(strings.TrimSpace(part), ";")
		if len(segments) < 2 {
			continue
		}
		rawURL := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(segments[0]), "<"), ">")
		for _, seg := range segments[1:] {
			if strings.TrimSpace(seg) == `rel="next"` {
				return rawURL
			}
		}
	}
	return ""
}

// resolveBypassedPushesForRuleset fetches each bypassed suite's own detail
// and keeps only the pushes where rulesetID's own rule -- not some other
// ruleset's -- is the one a rule evaluation shows actually failing (and
// therefore needed the bypass).
func resolveBypassedPushesForRuleset(ctx context.Context, client *http.Client, token, owner, repo string, rulesetID int64, suites []githubRuleSuiteSummary) ([]BypassedPush, error) {
	pushes := make([]BypassedPush, 0, len(suites))
	for _, suite := range suites {
		if suite.Result != "bypass" {
			continue
		}
		detail, err := getGitHubRuleSuiteDetail(ctx, client, token, owner, repo, suite.ID)
		if err != nil {
			return nil, err
		}
		rule, ok := bypassedRuleForRuleset(detail.RuleEvaluations, rulesetID)
		if !ok {
			continue
		}
		pushes = append(pushes, BypassedPush{
			RuleSuiteID: suite.ID, Actor: suite.ActorName, Commit: suite.AfterSHA,
			BeforeCommit: suite.BeforeSHA, PushedAt: suite.PushedAt, BypassedRule: rule,
		})
	}
	return pushes, nil
}

// bypassedRuleForRuleset returns the rule_type of the first evaluation that
// belongs to rulesetID and would have failed without the bypass.
func bypassedRuleForRuleset(evaluations []githubRuleEvaluation, rulesetID int64) (string, bool) {
	for _, eval := range evaluations {
		if eval.RuleSource.Type == "ruleset" && eval.RuleSource.ID == rulesetID && eval.Result == "fail" {
			return eval.RuleType, true
		}
	}
	return "", false
}

// listGitHubCompareCommits returns every commit a push added, oldest first --
// GitHub's own answer to "what did this push actually land", rather than an
// inference from the tip commit.
func listGitHubCompareCommits(ctx context.Context, client *http.Client, token, owner, repo, base, head string) ([]string, error) {
	if strings.TrimSpace(base) == "" || strings.TrimSpace(head) == "" {
		return nil, fmt.Errorf("the rule-suites ledger recorded no before/after pair for this push")
	}
	requestURL := fmt.Sprintf("%srepos/%s/%s/compare/%s...%s", registry.GithubAPIBaseURL, owner, repo, base, head)
	var payload struct {
		Commits []struct {
			SHA string `json:"sha"`
		} `json:"commits"`
	}
	if err := getGitHubJSON(ctx, client, token, requestURL, &payload); err != nil {
		return nil, err
	}
	commits := make([]string, 0, len(payload.Commits))
	for _, commit := range payload.Commits {
		commits = append(commits, commit.SHA)
	}
	return commits, nil
}

// listGitHubTagCommits maps each tag's own commit to the tag name, walking
// pagination up to maxReleaseTagPages.
func listGitHubTagCommits(ctx context.Context, client *http.Client, token, owner, repo string) (map[string]string, error) {
	tagByCommit := make(map[string]string)
	next := fmt.Sprintf("%srepos/%s/%s/tags?per_page=100", registry.GithubAPIBaseURL, owner, repo)
	for page := 0; next != "" && page < maxReleaseTagPages; page++ {
		var batch []struct {
			Name   string `json:"name"`
			Commit struct {
				SHA string `json:"sha"`
			} `json:"commit"`
		}
		link, err := getGitHubJSONPage(ctx, client, token, next, &batch)
		if err != nil {
			return nil, err
		}
		for _, tag := range batch {
			if tag.Commit.SHA != "" {
				tagByCommit[tag.Commit.SHA] = tag.Name
			}
		}
		next = link
	}
	return tagByCommit, nil
}

func getGitHubJSON(ctx context.Context, client *http.Client, token, requestURL string, into any) error {
	_, err := getGitHubJSONPage(ctx, client, token, requestURL, into)
	return err
}

// getGitHubJSONPage performs one authenticated GET, decodes it, and returns
// the rel="next" link so a paginated caller can keep walking.
func getGitHubJSONPage(ctx context.Context, client *http.Client, token, requestURL string, into any) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("build github request for %s: %w", requestURL, err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", requestURL, err)
	}
	body, readErr := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("read github response for %s: %w", requestURL, readErr)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("github returned %d for %s: %s", resp.StatusCode, requestURL, strings.TrimSpace(string(body)))
	}
	if err := json.Unmarshal(body, into); err != nil {
		return "", fmt.Errorf("decode github response for %s: %w", requestURL, err)
	}
	return githubNextPageLink(resp.Header.Get("Link")), nil
}

func getGitHubRuleSuiteDetail(ctx context.Context, client *http.Client, token, owner, repo string, id int64) (githubRuleSuiteDetail, error) {
	requestURL := fmt.Sprintf("%srepos/%s/%s/rulesets/rule-suites/%d", registry.GithubAPIBaseURL, owner, repo, id)
	var detail githubRuleSuiteDetail
	if err := getGitHubJSON(ctx, client, token, requestURL, &detail); err != nil {
		return githubRuleSuiteDetail{}, err
	}
	return detail, nil
}
