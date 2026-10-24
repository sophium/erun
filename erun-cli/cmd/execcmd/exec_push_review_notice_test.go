package execcmd

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/cloud"
	"github.com/sophium/erun/erun-common/infra/gitx"
	"github.com/sophium/erun/erun-common/infra/platformclient"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// The notice is additive: a successful push reports success whether or not a review exists.

const execPushNoticeAlias = "dev@erun"

type execPushNoticeStore struct {
	config configpkg.ERunConfig
}

func (s execPushNoticeStore) LoadERunConfig() (configpkg.ERunConfig, string, error) {
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
	return execPushNoticeStore{config: configpkg.ERunConfig{CloudProviders: []configpkg.CloudProviderConfig{{
		Alias:    execPushNoticeAlias,
		Provider: configpkg.CloudProviderERun,
		ERun:     &configpkg.ERunProviderConfig{APIURL: apiURL, ClientID: "cli-client-1"},
	}}}}
}

func execPushNoticeDependencies(t *testing.T) cloud.CloudDependencies {
	t.Helper()
	cached, err := json.Marshal(struct {
		AccessToken string    `json:"accessToken"`
		ExpiresAt   time.Time `json:"expiresAt"`
	}{AccessToken: "exec-push-token", ExpiresAt: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatalf("encode cached access token: %v", err)
	}
	return cloud.CloudDependencies{CloudSecretStore: execPushNoticeSecrets{
		secrets: map[string]string{"erun/access/" + execPushNoticeAlias: string(cached)},
	}}
}

func execPushNoticePushDependencies(branch string) gitx.PushWorkingTreeBranchDependencies {
	return gitx.PushWorkingTreeBranchDependencies{
		CurrentBranch: func(core.Context, string) (string, error) { return branch, nil },
		CurrentCommit: func(core.Context, string) (string, error) { return "0123456789abcdef", nil },
		RunGit:        func(string, io.Writer, io.Writer, ...string) error { return nil },
	}
}

func execPushNoticeFinder(projectRoot string) shared.ProjectFinderFunc {
	return func() (string, string, error) { return "tenant", projectRoot, nil }
}

func execPushNoticeContext(out *bytes.Buffer) core.Context {
	return core.Context{
		Logger: core.NewLoggerWithWriters(core.VerbosityInfo, out, out),
		Output: core.OutputText,
		Stdout: out,
		Stderr: out,
	}
}

func TestExecPushStillSucceedsAndWarnsWhenTheBranchHasNoReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{})
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

func TestExecPushStaysSilentWhenTheBranchHasAReview(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]platformclient.PlatformReview{
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

func TestExecPushIgnoresAMissingPlatformAlias(t *testing.T) {
	out := &bytes.Buffer{}
	err := runExecPushCommand(
		execPushNoticeContext(out),
		execPushNoticeFinder(t.TempDir()),
		execPushNoticeStore{},
		cloud.CloudDependencies{},
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
