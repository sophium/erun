package release

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

// writeModuleFixture references imageName through an interpolation, as the real module does.
func writeModuleFixture(t *testing.T, root, imageName string) {
	t.Helper()
	dir := filepath.Join(root, "erun-devops", "terraform-erun", "modules", "terraform-erun-fixture")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir fixture module dir: %v", err)
	}
	content := fmt.Sprintf(`locals {
  fixture_image = local.arg_fixture_image != "" ? local.arg_fixture_image : "ghcr.io/sophium/%s:${local.fixture_chart_app_version}"
}
`, imageName)
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture main.tf: %v", err)
	}
}

func TestDiscoverModuleReferencedImageNames(t *testing.T) {
	t.Run("a root with no terraform-erun tree yields no names, not an error", func(t *testing.T) {
		names, err := DiscoverModuleReferencedImageNames(filepath.Join(t.TempDir(), "erun-devops", "terraform-erun"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(names) != 0 {
			t.Fatalf("names = %v, want none", names)
		}
	})
}

// anonymousManifestStub records what credentials the token and manifest requests carried.
type anonymousManifestStub struct {
	ManifestStatus int
	TokenStatus    int

	tokenRequestSawBasicAuth  bool
	tokenRequestSawAnyAuthHdr bool
	manifestAuthHeader        string
}

func (s *anonymousManifestStub) start(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		if _, _, ok := r.BasicAuth(); ok {
			s.tokenRequestSawBasicAuth = true
		}
		if r.Header.Get("Authorization") != "" {
			s.tokenRequestSawAnyAuthHdr = true
		}
		if s.TokenStatus != 0 && s.TokenStatus != http.StatusOK {
			w.WriteHeader(s.TokenStatus)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": "anon-token"})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		s.manifestAuthHeader = r.Header.Get("Authorization")
		status := s.ManifestStatus
		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

type probeStatusCase struct {
	name           string
	tokenStatus    int
	manifestStatus int
	wantPullable   bool
	wantErr        bool
}

var probeStatusCases = []probeStatusCase{
	{name: "pullable manifest reports true", manifestStatus: http.StatusOK, wantPullable: true},
	{name: "denied manifest reports false, not an error", manifestStatus: http.StatusForbidden},
	// ghcr refuses a private package's anonymous token request with 401: a conclusive denial.
	{name: "401 on the token request reports false, not an error", tokenStatus: http.StatusUnauthorized},
	{name: "403 on the token request reports false, not an error", tokenStatus: http.StatusForbidden},
	{name: "a non-auth manifest failure is an error, not a conclusive denial", manifestStatus: http.StatusInternalServerError, wantErr: true},
}

func TestProbeAnonymousManifestPullAtStatusHandling(t *testing.T) {
	for _, tc := range probeStatusCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &anonymousManifestStub{ManifestStatus: tc.manifestStatus, TokenStatus: tc.tokenStatus}
			server := stub.start(t)

			pullable, err := shared.ProbeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if pullable != tc.wantPullable {
				t.Fatalf("pullable = %v, want %v", pullable, tc.wantPullable)
			}
		})
	}
}

func TestProbeAnonymousManifestPullAtTokenRequestUnreachable(t *testing.T) {
	unreachable := "http://127.0.0.1:1"
	_, err := shared.ProbeAnonymousManifestPullAt(context.Background(), &http.Client{Timeout: time.Second}, "sophium/erun-example", "1.0.0", unreachable, unreachable)
	if err == nil {
		t.Fatal("expected an error: the probe could not reach the registry at all")
	}
}

// `docker manifest inspect` uses local credentials, so this probe must send none.
func TestProbeAnonymousManifestPullAtSendsNoCredentials(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("sophium:tok")))
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) { return "leaked-gh-token", true })

	stub := &anonymousManifestStub{ManifestStatus: http.StatusOK}
	server := stub.start(t)

	if _, err := shared.ProbeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if stub.tokenRequestSawBasicAuth {
		t.Fatal("the token exchange must never send Basic auth, even when a docker credential is configured on this machine")
	}
	if stub.tokenRequestSawAnyAuthHdr {
		t.Fatal("the token exchange must carry no Authorization header at all")
	}
	if stub.manifestAuthHeader != "Bearer anon-token" {
		t.Fatalf("manifest request Authorization = %q, want the anonymously-minted token, never a stored credential", stub.manifestAuthHeader)
	}
}

func fakeExecutionForImage(imageName, tag, version string) shared.BuildExecutionSpec {
	return shared.BuildExecutionSpec{
		DockerPushes: []shared.DockerPushSpec{
			{Image: model.DockerImageReference{ImageName: imageName, Tag: tag, Version: version}},
		},
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePasses(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	var logBuf strings.Builder
	ctx := core.Context{Logger: core.NewLogger(0).WithTraceSink(&logBuf)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return true, nil }
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(logBuf.String(), "Verified anonymously pullable: ghcr.io/sophium/erun-example-webhook:1.2.3") {
		t.Fatalf("expected a verified-pullable report, got log:\n%s", logBuf.String())
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenNotBaselined(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := core.Context{Logger: core.NewLogger(0)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe)
	if err == nil {
		t.Fatal("expected an error: a fresh, unbaselined gap must fail the release")
	}
	if !strings.Contains(err.Error(), "ghcr.io/sophium/erun-example-webhook:1.2.3") {
		t.Fatalf("error should name the offending image, got: %v", err)
	}
}

// withAnonymousPullabilityBaseline overrides the package-level baseline for one test.
func withAnonymousPullabilityBaseline(t *testing.T, baseline map[string]bool) {
	t.Helper()
	original := AnonymousPullabilityBaseline
	AnonymousPullabilityBaseline = baseline
	t.Cleanup(func() { AnonymousPullabilityBaseline = original })
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselined(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.217", "1.0.217")
	var logBuf strings.Builder
	ctx := core.Context{Logger: core.NewLogger(0).WithTraceSink(&logBuf)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("a baselined gap must not fail the release: %v", err)
	}
	if !strings.Contains(logBuf.String(), "Not anonymously pullable (baselined") {
		t.Fatalf("expected the baselined gap to be reported, got log:\n%s", logBuf.String())
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenBaselinedButNowPullable(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.221", "1.0.221")
	ctx := core.Context{Logger: core.NewLogger(0)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return true, nil }
	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe)
	if err == nil {
		t.Fatal("expected an error: a baselined image that now probes as pullable means the baseline entry is stale")
	}
	if !strings.Contains(err.Error(), "erun-baselined-webhook") {
		t.Fatalf("error should name the stale entry, got: %v", err)
	}
	if !strings.Contains(err.Error(), "anonymousPullabilityBaseline") {
		t.Fatalf("error should point at anonymousPullabilityBaseline, got: %v", err)
	}
}

func realProbeAt(server *httptest.Server) shared.AnonymousPullProbeFunc {
	return func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error) {
		return shared.ProbeAnonymousManifestPullAt(ctx, server.Client(), repoPath, tag, server.URL, server.URL)
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselinedTokenRequestRefused(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.219", "1.0.219")
	var logBuf strings.Builder
	ctx := core.Context{Logger: core.NewLogger(0).WithTraceSink(&logBuf)}

	stub := &anonymousManifestStub{TokenStatus: http.StatusUnauthorized}
	server := stub.start(t)

	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, realProbeAt(server)); err != nil {
		t.Fatalf("a baselined image's token-request refusal must not fail the release: %v", err)
	}
	if !strings.Contains(logBuf.String(), "Not anonymously pullable (baselined") {
		t.Fatalf("expected the baselined refusal to be reported, got log:\n%s", logBuf.String())
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenNotBaselinedTokenRequestRefused(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := core.Context{Logger: core.NewLogger(0)}

	stub := &anonymousManifestStub{TokenStatus: http.StatusUnauthorized}
	server := stub.start(t)

	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, realProbeAt(server))
	if err == nil {
		t.Fatal("expected an error: an unbaselined private image must fail the release")
	}
	if !strings.Contains(err.Error(), "ghcr.io/sophium/erun-example-webhook:1.2.3") {
		t.Fatalf("error should name the offending image, got: %v", err)
	}
	if !strings.Contains(err.Error(), "make the ghcr.io/sophium/erun-example-webhook package public") {
		t.Fatalf("error should offer the make-public remedy, got: %v", err)
	}
	if !strings.Contains(err.Error(), "anonymousPullabilityBaseline") {
		t.Fatalf("error should offer the baseline remedy, got: %v", err)
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenNotBaselinedProbeUnreachable(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := core.Context{Logger: core.NewLogger(0)}

	unreachable := "http://127.0.0.1:1"
	probe := func(ctx context.Context, _ *http.Client, repoPath, tag string) (bool, error) {
		return shared.ProbeAnonymousManifestPullAt(ctx, &http.Client{Timeout: time.Second}, repoPath, tag, unreachable, unreachable)
	}

	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe)
	if err == nil {
		t.Fatal("expected an error: the probe could not run at all")
	}
	if !strings.Contains(err.Error(), "probe anonymous pull of") {
		t.Fatalf("error should say the probe could not run, not that the image is private, got: %v", err)
	}
	if strings.Contains(err.Error(), "resolves for the publishing credential but not anonymously") {
		t.Fatalf("error must not claim the image is private when the probe never got an answer, got: %v", err)
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselinedAndProbeUnreachable(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.219", "1.0.219")
	var logBuf strings.Builder
	ctx := core.Context{Logger: core.NewLogger(0).WithTraceSink(&logBuf)}

	unreachable := "http://127.0.0.1:1"
	probe := func(ctx context.Context, _ *http.Client, repoPath, tag string) (bool, error) {
		return shared.ProbeAnonymousManifestPullAt(ctx, &http.Client{Timeout: time.Second}, repoPath, tag, unreachable, unreachable)
	}

	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("a baselined image must not fail the release when the probe cannot even run: %v", err)
	}
	if !strings.Contains(logBuf.String(), "could not run") {
		t.Fatalf("expected the unreachable probe to be reported, got log:\n%s", logBuf.String())
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenUnpublished(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-orphaned-webhook")
	execution := shared.BuildExecutionSpec{}
	ctx := core.Context{Logger: core.NewLogger(0)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) {
		t.Fatal("must not probe an image nothing publishes")
		return false, nil
	}
	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe)
	if err == nil || !strings.Contains(err.Error(), "erun-orphaned-webhook") {
		t.Fatalf("expected an error naming the unpublished reference, got: %v", err)
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableNoOpWhenNothingReferenced(t *testing.T) {
	root := t.TempDir()
	ctx := core.Context{Logger: core.NewLogger(0)}
	probe := func(context.Context, *http.Client, string, string) (bool, error) {
		t.Fatal("must not probe when nothing is referenced")
		return false, nil
	}
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, shared.BuildExecutionSpec{}, probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnonymousPullabilityBaselineIsCurrent(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	names, err := DiscoverModuleReferencedImageNames(filepath.Join(root, "erun-devops", "terraform-erun"))
	if err != nil {
		t.Fatalf("scan terraform modules: %v", err)
	}
	referenced := make(map[string]bool, len(names))
	for _, name := range names {
		referenced[name] = true
	}
	for name := range AnonymousPullabilityBaseline {
		if !referenced[name] {
			t.Errorf("anonymousPullabilityBaseline names %q, but no terraform module references it any more -- remove the stale entry", name)
		}
	}
}
