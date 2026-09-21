package eruncommon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// pushNoticeStore is the smallest CloudReadStore the notice needs: a config
// that either has an erun platform alias or has none at all, which is the
// distinction that decides whether the check runs.
type pushNoticeStore struct {
	config ERunConfig
}

func (s pushNoticeStore) LoadERunConfig() (ERunConfig, string, error) { return s.config, "", nil }

// pushNoticeSecrets is a CloudSecretStore holding one alias's cached access
// token, so the lookup reaches the test server without minting anything: a
// cached, unexpired token is what resolveERunAccessToken returns before it
// ever considers a refresh grant.
type pushNoticeSecrets struct {
	secrets map[string]string
}

func (s pushNoticeSecrets) SaveCloudSecret(ref, value string) error {
	s.secrets[ref] = value
	return nil
}

func (s pushNoticeSecrets) LoadCloudSecret(ref string) (string, error) {
	return s.secrets[ref], nil
}
func (s pushNoticeSecrets) DeleteCloudSecret(ref string) error { delete(s.secrets, ref); return nil }

const pushNoticeAlias = "dev@erun"

func pushNoticeStoreWithAlias(apiURL string) pushNoticeStore {
	return pushNoticeStore{config: ERunConfig{CloudProviders: []CloudProviderConfig{{
		Alias:    pushNoticeAlias,
		Provider: CloudProviderERun,
		ERun:     &ERunProviderConfig{APIURL: apiURL, ClientID: "cli-client-1"},
	}}}}
}

// pushNoticeDeps wires the cached token the lookup will authenticate with.
func pushNoticeDeps(t *testing.T) CloudDependencies {
	t.Helper()
	cached, err := json.Marshal(erunAccessTokenCache{
		AccessToken: "push-notice-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encode cached access token: %v", err)
	}
	return CloudDependencies{CloudSecretStore: pushNoticeSecrets{
		secrets: map[string]string{erunAccessTokenCacheRef(pushNoticeAlias): string(cached)},
	}}
}

// pushNoticeContext is the CLI's own wiring: a push's diagnostics reach the
// operator on stderr, so both of the logger's writers point at the one buffer
// the test reads -- which is exactly where the warning is asserted to land.
func pushNoticeContext(out *bytes.Buffer) Context {
	return Context{Logger: NewLoggerWithWriters(VerbosityInfo, out, out)}
}

func pushNoticeParams(branch string) PushedBranchReviewNoticeParams {
	return PushedBranchReviewNoticeParams{Branch: branch, Remote: "origin"}
}

// pushNoticeWarningNeedle is the phrase only the warning carries, so a test
// can assert on the warning alone rather than on the whole stream -- which
// also holds the trace lines the same command legitimately prints.
const pushNoticeWarningNeedle = "no review references it"

// TestWarnPushedBranchWithoutReviewWarnsWhenNoReviewReferencesTheBranch is the
// reproduction of the reported failure: a branch is pushed, the platform holds
// no review for it, and nothing said so. The warning must name the branch and
// the command that queues it.
func TestWarnPushedBranchWithoutReviewWarnsWhenNoReviewReferencesTheBranch(t *testing.T) {
	var requestedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer push-notice-token" {
			t.Errorf("Authorization = %q, want the cached bearer token", r.Header.Get("Authorization"))
		}
		requestedQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]PlatformReview{})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-orphan"))

	want := "warning: pushed `feature/2204-orphan`; no review references it — `erun review create` to queue it\n"
	if got := out.String(); !strings.HasSuffix(got, want) {
		t.Fatalf("stderr = %q, want it to end with %q", got, want)
	}
	// The branch is matched by asking the platform for the review that
	// references it, not by inferring one from the branch name.
	if !strings.Contains(requestedQuery, "sourceBranch=feature%2F2204-orphan") {
		t.Fatalf("query = %q, want it narrowed to the pushed branch's source branch", requestedQuery)
	}
}

// TestWarnPushedBranchWithoutReviewSilentWhenAReviewReferencesTheBranch is the
// other half of the same state: a queued branch must never be reported as an
// orphan, whatever queue state its review is in.
func TestWarnPushedBranchWithoutReviewSilentWhenAReviewReferencesTheBranch(t *testing.T) {
	for _, status := range []string{"OPEN", "READY", "MERGE", "MERGED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode([]PlatformReview{{ReviewID: "r1", Name: "Add widget", SourceBranch: "feature/2204-queued", Status: status}})
			}))
			defer srv.Close()

			out := &bytes.Buffer{}
			WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-queued"))
			if strings.Contains(out.String(), pushNoticeWarningNeedle) {
				t.Fatalf("a review in %s still produced %q", status, out.String())
			}
		})
	}
}

// TestWarnPushedBranchWithoutReviewTreatsAClosedReviewAsNoReview pins the one
// state that is not a reference: a closed review queues nothing, so the branch
// is an orphan again.
func TestWarnPushedBranchWithoutReviewTreatsAClosedReviewAsNoReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]PlatformReview{{ReviewID: "r1", SourceBranch: "feature/2204-closed", Status: "CLOSED"}})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-closed"))
	if !strings.Contains(out.String(), pushNoticeWarningNeedle) {
		t.Fatalf("a closed review suppressed the warning: %q", out.String())
	}
}

// TestWarnPushedBranchWithoutReviewMatchesTheSourceBranch guards against a
// plane that answers with reviews for other branches -- reviews are named
// after the eventual squash message, so only sourceBranch identifies one.
func TestWarnPushedBranchWithoutReviewMatchesTheSourceBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]PlatformReview{
			{ReviewID: "r1", Name: "feature/2204-orphan", SourceBranch: "feature/2204-other", Status: "OPEN"},
		})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-orphan"))
	if !strings.Contains(out.String(), pushNoticeWarningNeedle) {
		t.Fatalf("a review of another branch suppressed the warning: %q", out.String())
	}
}

// TestWarnPushedBranchWithoutReviewSkipsTheRemoteDefaultBranch pins that
// pushing the branch work targets is not an orphan: the check must not even
// ask, since no review can reference it as a source.
func TestWarnPushedBranchWithoutReviewSkipsTheRemoteDefaultBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the check queried the platform for the default branch: %s", r.URL.String())
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("main"))
	if strings.Contains(out.String(), pushNoticeWarningNeedle) {
		t.Fatalf("pushing the default branch produced %q", out.String())
	}
}

// TestWarnPushedBranchWithoutReviewSilentWithoutAnAlias is the contract that
// keeps every machine with no platform configured on exactly today's push:
// no network call, no output at all, not even a trace line.
func TestWarnPushedBranchWithoutReviewSilentWithoutAnAlias(t *testing.T) {
	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStore{}, CloudDependencies{}, pushNoticeParams("feature/2204-orphan"))
	if out.Len() != 0 {
		t.Fatalf("a machine with no platform alias produced %q", out.String())
	}
}

// TestWarnPushedBranchWithoutReviewGivesUpQuietlyWhenThePlaneFails pins the
// best-effort contract against the flakiness that motivated it: an unreachable
// or failing plane must leave the push exactly as it was, with a trace line
// and no warning, because a notice is never worth breaking a push that landed.
func TestWarnPushedBranchWithoutReviewGivesUpQuietlyWhenThePlaneFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		handler http.HandlerFunc
	}{
		{name: "server error", handler: func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
		}},
		{name: "malformed body", handler: func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("not json"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(tc.handler)
			defer srv.Close()

			out := &bytes.Buffer{}
			WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-orphan"))
			if strings.Contains(out.String(), pushNoticeWarningNeedle) {
				t.Fatalf("a failed lookup produced %q", out.String())
			}
			if !strings.Contains(out.String(), "push review notice skipped") {
				t.Fatalf("a failed lookup was silent rather than traced: %q", out.String())
			}
		})
	}
}

// TestWarnPushedBranchWithoutReviewTracesWithoutQueryingUnderDryRun keeps the
// dry-run contract: the check is traced as the call it would make, and nothing
// is sent.
func TestWarnPushedBranchWithoutReviewTracesWithoutQueryingUnderDryRun(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("a dry run queried the platform: %s", r.URL.String())
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	ctx := pushNoticeContext(out)
	ctx.DryRun = true
	WarnPushedBranchWithoutReview(ctx, pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-orphan"))

	traced := out.String()
	if !strings.Contains(traced, "GET "+srv.URL+"/v1/reviews") || !strings.Contains(traced, "sourceBranch=feature/2204-orphan") {
		t.Fatalf("dry run trace = %q, want the resolved review lookup", traced)
	}
	if strings.Contains(traced, pushNoticeWarningNeedle) {
		t.Fatalf("a dry run warned about an unverified state: %q", traced)
	}
}
