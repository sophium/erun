package cmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	common "github.com/sophium/erun/erun-common"
)

// The push itself is stubbed at the git layer so the whole command body runs:
// what these cases pin is the command's contract around the notice -- the
// notice is additive, so a push that succeeded must still report success and
// return a nil error whether or not a review exists for the branch.

const execPushNoticeAlias = "dev@erun"

type execPushNoticeStore struct {
	config common.ERunConfig
}

func (s execPushNoticeStore) LoadERunConfig() (common.ERunConfig, string, error) {
	return s.config, "", nil
}

type execPushNoticeSecrets struct {
	secrets map[string]string
}

func (s execPushNoticeSecrets) SaveCloudSecret(ref, value string) error {
	s.secrets[ref] = value
	return nil
}

func (s execPushNoticeSecrets) LoadCloudSecret(ref string) (string, error) {
	return s.secrets[ref], nil
}

func (s execPushNoticeSecrets) DeleteCloudSecret(ref string) error {
	delete(s.secrets, ref)
	return nil
}

func execPushNoticeStoreWithAlias(apiURL string) execPushNoticeStore {
	return execPushNoticeStore{config: common.ERunConfig{CloudProviders: []common.CloudProviderConfig{{
		Alias:    execPushNoticeAlias,
		Provider: common.CloudProviderERun,
		ERun:     &common.ERunProviderConfig{APIURL: apiURL, ClientID: "cli-client-1"},
	}}}}
}

// execPushNoticeDependencies carries the cached access token the notice
// authenticates with, so the lookup reaches the test server without a login.
func execPushNoticeDependencies(t *testing.T) common.CloudDependencies {
	t.Helper()
	cached, err := json.Marshal(struct {
		AccessToken string    `json:"accessToken"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}{AccessToken: "exec-push-token", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode cached access token: %v", err)
	}
	return common.CloudDependencies{CloudSecretStore: execPushNoticeSecrets{
		secrets: map[string]string{"erun/access/" + execPushNoticeAlias: string(cached)},
	}}
}

// execPushNoticePushDependencies is the stubbed push: the tree is on the
// requested branch, and the push and its read-back report that same branch, so
// the command body reaches the notice with a real result.
func execPushNoticePushDependencies(branch string) common.PushWorkingTreeBranchDependencies {
	return common.PushWorkingTreeBranchDependencies{
		CurrentBranch: func(common.Context, string) (string, error) { return branch, nil },
		CurrentCommit: func(common.Context, string) (string, error) { return "0123456789abcdef", nil },
		RunGit:        func(string, io.Writer, io.Writer, ...string) error { return nil },
	}
}

func execPushNoticeFinder(projectRoot string) common.ProjectFinderFunc {
	return func() (string, string, error) { return "tenant", projectRoot, nil }
}

// execPushNoticeContext mirrors the CLI's own wiring: a push's diagnostics --
// including the warning -- reach the operator on stderr, which is where the
// logger's writers point.
func execPushNoticeContext(out *bytes.Buffer) common.Context {
	return common.Context{
		Logger: common.NewLoggerWithWriters(common.VerbosityInfo, out, out),
		Output: common.OutputText,
		Stdout: out,
		Stderr: out,
	}
}

// TestExecPushStillSucceedsAndWarnsWhenTheBranchHasNoReview is the command-half
// reproduction: the pushed branch has no review, the push must still report
// success with a nil error, and the warning must reach stderr.
func TestExecPushStillSucceedsAndWarnsWhenTheBranchHasNoReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]common.PlatformReview{})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	err := runExecPushCommand(
		execPushNoticeContext(out),
		execPushNoticeFinder(t.TempDir()),
		execPushNoticeStoreWithAlias(srv.URL),
		execPushNoticeDependencies(t),
		execPushNoticePushDependencies("feature/2204-orphan"),
		"feature/2204-orphan",
		"origin",
	)
	if err != nil {
		t.Fatalf("a branch with no review failed the push: %v", err)
	}
	if !strings.Contains(out.String(), "warning: pushed `feature/2204-orphan`; no review references it") {
		t.Fatalf("stderr = %q, want the unqueued-branch warning", out.String())
	}
}

// TestExecPushStaysSilentWhenTheBranchHasAReview is the other half: the same
// command over a branch a review already references must say nothing beyond
// its own success line.
func TestExecPushStaysSilentWhenTheBranchHasAReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]common.PlatformReview{
			{ReviewID: "r1", SourceBranch: "feature/2204-queued", Status: "READY"},
		})
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	err := runExecPushCommand(
		execPushNoticeContext(out),
		execPushNoticeFinder(t.TempDir()),
		execPushNoticeStoreWithAlias(srv.URL),
		execPushNoticeDependencies(t),
		execPushNoticePushDependencies("feature/2204-queued"),
		"feature/2204-queued",
		"origin",
	)
	if err != nil {
		t.Fatalf("a queued branch failed the push: %v", err)
	}
	if strings.Contains(out.String(), "no review references it") {
		t.Fatalf("a queued branch produced the warning: %q", out.String())
	}
}

// TestExecPushIgnoresAMissingPlatformAlias entirely: an environment with no
// erun platform configured must push exactly as it did before the notice
// existed -- no warning, no trace line about a check, no failure.
func TestExecPushIgnoresAMissingPlatformAlias(t *testing.T) {
	out := &bytes.Buffer{}
	err := runExecPushCommand(
		execPushNoticeContext(out),
		execPushNoticeFinder(t.TempDir()),
		execPushNoticeStore{},
		common.CloudDependencies{},
		execPushNoticePushDependencies("feature/2204-orphan"),
		"feature/2204-orphan",
		"origin",
	)
	if err != nil {
		t.Fatalf("an unconfigured environment failed the push: %v", err)
	}
	traced := out.String()
	if strings.Contains(traced, "no review references it") || strings.Contains(traced, "push review notice") {
		t.Fatalf("an unconfigured environment saw notice output: %q", traced)
	}
}

// TestExecPushSurvivesAnUnreachablePlatform pins the best-effort contract at
// the command boundary: the plane refusing the lookup is a skipped notice, not
// a failed push.
func TestExecPushSurvivesAnUnreachablePlatform(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"boom"}`, http.StatusInternalServerError)
	}))
	defer srv.Close()

	out := &bytes.Buffer{}
	err := runExecPushCommand(
		execPushNoticeContext(out),
		execPushNoticeFinder(t.TempDir()),
		execPushNoticeStoreWithAlias(srv.URL),
		execPushNoticeDependencies(t),
		execPushNoticePushDependencies("feature/2204-orphan"),
		"feature/2204-orphan",
		"origin",
	)
	if err != nil {
		t.Fatalf("a failing platform lookup failed the push: %v", err)
	}
	if strings.Contains(out.String(), "no review references it") {
		t.Fatalf("an unavailable platform produced a warning: %q", out.String())
	}
}
