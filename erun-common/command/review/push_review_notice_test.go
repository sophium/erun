package review

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/platformclient"
)

type pushNoticeStore struct {
	config configpkg.ERunConfig
}

func (s pushNoticeStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
	return s.config, "", nil
}

// A cached unexpired token is returned before any refresh grant is considered.
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
	return pushNoticeStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:    pushNoticeAlias,
		Provider: configpkg.CloudProviderERun,
		ERun:     &configpkg.ERunProviderConfig{APIURL: apiURL, ClientID: "cli-client-1"},
	}}}}
}

func pushNoticeDeps(t *testing.T) cloud.CloudDependencies {
	t.Helper()
	cached, err := json.Marshal(cloud.ErunAccessTokenCache{
		AccessToken: "push-notice-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("encode cached access token: %v", err)
	}
	return cloud.CloudDependencies{CloudSecretStore: pushNoticeSecrets{
		secrets: map[string]string{cloud.ErunAccessTokenCacheRef(pushNoticeAlias): string(cached)},
	}}
}

// pushNoticeContext points both logger writers at one buffer, as the CLI
// sends push diagnostics to stderr.
func pushNoticeContext(out *bytes.Buffer) core.Context {
	return core.Context{Logger: core.NewLoggerWithWriters(core.VerbosityInfo, out, out)}
}

func pushNoticeParams(branch string) PushedBranchReviewNoticeParams {
	return PushedBranchReviewNoticeParams{Branch: branch, Remote: "origin"}
}

// pushNoticeWarningNeedle separates the warning from the command's trace lines.
const pushNoticeWarningNeedle = "no review references it"

func TestWarnPushedBranchWithoutReviewWarnsWhenNoReviewReferencesTheBranch(t *testing.T) {
	var requestedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer push-notice-token" {
			t.Errorf("Authorization = %q, want the cached bearer token", r.Header.Get("Authorization"))
		}
		requestedQuery = r.URL.RawQuery
		_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-orphan"))

	want := "warning: pushed `feature/2204-orphan`; no review references it — `erun review create` to queue it\n"
	if got := out.String(); !strings.HasSuffix(got, want) {
		t.Fatalf("stderr = %q, want it to end with %q", got, want)
	}
	if !strings.Contains(requestedQuery, "sourceBranch=feature%2F2204-orphan") {
		t.Fatalf("query = %q, want it narrowed to the pushed branch's source branch", requestedQuery)
	}
}

func TestWarnPushedBranchWithoutReviewSilentWhenAReviewReferencesTheBranch(t *testing.T) {
	for _, status := range []string{"OPEN", "READY", "MERGE", "MERGED", "FAILED"} {
		t.Run(status, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{{ReviewID: "r1", Name: "Add widget", SourceBranch: "feature/2204-queued", Status: status}})
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

func TestWarnPushedBranchWithoutReviewTreatsAClosedReviewAsNoReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{{ReviewID: "r1", SourceBranch: "feature/2204-closed", Status: "CLOSED"}})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStoreWithAlias(srv.URL), pushNoticeDeps(t), pushNoticeParams("feature/2204-closed"))
	if !strings.Contains(out.String(), pushNoticeWarningNeedle) {
		t.Fatalf("a closed review suppressed the warning: %q", out.String())
	}
}

// Reviews are named after the squash message, so only sourceBranch identifies one.
func TestWarnPushedBranchWithoutReviewMatchesTheSourceBranch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{
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

// No alias means no network call and no output at all, not even a trace line.
func TestWarnPushedBranchWithoutReviewSilentWithoutAnAlias(t *testing.T) {
	out := &bytes.Buffer{}
	WarnPushedBranchWithoutReview(pushNoticeContext(out), pushNoticeStore{}, cloud.CloudDependencies{}, pushNoticeParams("feature/2204-orphan"))
	if out.Len() != 0 {
		t.Fatalf("a machine with no platform alias produced %q", out.String())
	}
}

// A failing plane leaves a trace line and no warning.
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
