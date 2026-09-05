package eruncommon

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
)

// writeModuleFixture lays down a single Terraform file referencing imageName
// the way terraform-erun-cluster-edge/main.tf references
// ghcr.io/sophium/erun-dns01-webhook: through an interpolation, not a
// literal version, so the fixture also proves the pattern does not depend on
// a literal tag being present.
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
	t.Run("finds a name behind an interpolated version", func(t *testing.T) {
		root := t.TempDir()
		writeModuleFixture(t, root, "erun-example-webhook")

		names, err := discoverModuleReferencedImageNames(filepath.Join(root, "erun-devops", "terraform-erun"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(names) != 1 || names[0] != "erun-example-webhook" {
			t.Fatalf("names = %v, want [erun-example-webhook]", names)
		}
	})

	t.Run("a root with no terraform-erun tree yields no names, not an error", func(t *testing.T) {
		names, err := discoverModuleReferencedImageNames(filepath.Join(t.TempDir(), "erun-devops", "terraform-erun"))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(names) != 0 {
			t.Fatalf("names = %v, want none", names)
		}
	})
}

// anonymousManifestStub answers the two requests a manifest pull makes: the
// GHCR token exchange and the manifest GET. It records whether the token
// request carried any credential at all, and what Authorization header the
// manifest request actually sent, so a test can assert the probe is
// genuinely credential-free rather than inferring it from a passing status.
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

// probeStatusCase is one status-handling scenario for probeAnonymousManifestPullAt:
// a token-endpoint status, a manifest-endpoint status, and the answer the
// probe must resolve to.
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
	// This is the 1.0.219 regression: a private ghcr package refuses the
	// anonymous token request itself with 401, before a manifest request is
	// even made. That refusal is a conclusive answer -- "a stranger cannot
	// pull this" -- not a failure to reach the registry, so it must resolve
	// like any other denial: false, no error.
	{name: "401 on the token request reports false, not an error", tokenStatus: http.StatusUnauthorized},
	{name: "403 on the token request reports false, not an error", tokenStatus: http.StatusForbidden},
	// A non-auth manifest status is not a conclusive answer about
	// pullability, so it must keep erroring rather than being folded into
	// "not pullable".
	{name: "a non-auth manifest failure is an error, not a conclusive denial", manifestStatus: http.StatusInternalServerError, wantErr: true},
}

func TestProbeAnonymousManifestPullAtStatusHandling(t *testing.T) {
	for _, tc := range probeStatusCases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &anonymousManifestStub{ManifestStatus: tc.manifestStatus, TokenStatus: tc.tokenStatus}
			server := stub.start(t)

			pullable, err := probeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL)
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

// A registry that cannot even be reached is genuinely inconclusive -- it must
// keep erroring rather than being folded into "not pullable", or a network
// blip during release would silently pass this check.
func TestProbeAnonymousManifestPullAtTokenRequestUnreachable(t *testing.T) {
	unreachable := "http://127.0.0.1:1"
	_, err := probeAnonymousManifestPullAt(context.Background(), &http.Client{Timeout: time.Second}, "sophium/erun-example", "1.0.0", unreachable, unreachable)
	if err == nil {
		t.Fatal("expected an error: the probe could not reach the registry at all")
	}
}

// This is the regression for the defect release-time verification used to
// have: verifyPublishedReleaseArtifacts re-resolves a manifest through
// `docker manifest inspect`, which authenticates with whatever the local
// daemon has stored, so it can never observe whether a stranger with no
// credential at all could pull the same image. This probe must never send
// one, in either direction: not on the token exchange, and not folded into
// the manifest request's own Authorization header.
func TestProbeAnonymousManifestPullAtSendsNoCredentials(t *testing.T) {
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("sophium:tok")))
	useDockerConfigDir(t, dir)
	useGHToken(t, func(string) (string, bool) { return "leaked-gh-token", true })

	stub := &anonymousManifestStub{ManifestStatus: http.StatusOK}
	server := stub.start(t)

	if _, err := probeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL); err != nil {
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

func fakeExecutionForImage(imageName, tag, version string) BuildExecutionSpec {
	return BuildExecutionSpec{
		dockerPushes: []DockerPushSpec{
			{Image: DockerImageReference{ImageName: imageName, Tag: tag, Version: version}},
		},
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePasses(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	var logBuf strings.Builder
	ctx := Context{Logger: NewLogger(0).WithTraceSink(&logBuf)}

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
	ctx := Context{Logger: NewLogger(0)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe)
	if err == nil {
		t.Fatal("expected an error: a fresh, unbaselined gap must fail the release")
	}
	if !strings.Contains(err.Error(), "ghcr.io/sophium/erun-example-webhook:1.2.3") {
		t.Fatalf("error should name the offending image, got: %v", err)
	}
}

// withAnonymousPullabilityBaseline overrides the package-level baseline for
// the duration of a test, restoring the original afterward. The real baseline
// is expected to be empty most of the time (erun-common/AGENTS.md's
// dependency-injection-over-globals preference doesn't apply cleanly to a
// shrink-only map that production code also reads by name), so tests that
// need a baselined entry inject their own fixture name instead of depending
// on whatever the real map currently holds.
func withAnonymousPullabilityBaseline(t *testing.T, baseline map[string]bool) {
	t.Helper()
	original := anonymousPullabilityBaseline
	anonymousPullabilityBaseline = baseline
	t.Cleanup(func() { anonymousPullabilityBaseline = original })
}

func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselined(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.217", "1.0.217")
	var logBuf strings.Builder
	ctx := Context{Logger: NewLogger(0).WithTraceSink(&logBuf)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) { return false, nil }
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("a baselined gap must not fail the release: %v", err)
	}
	if !strings.Contains(logBuf.String(), "Not anonymously pullable (baselined") {
		t.Fatalf("expected the baselined gap to be reported, got log:\n%s", logBuf.String())
	}
}

// This is the property erun#1587 found missing: a baselined image is a
// record that the package is *currently* private, not a promise it will stay
// that way. Once it probes as anonymously pullable, the entry is a lie that
// would otherwise let this check silently no-op forever, so the release must
// fail and name the stale entry to remove.
func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenBaselinedButNowPullable(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.221", "1.0.221")
	ctx := Context{Logger: NewLogger(0)}

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

// realProbeAt closes over a stub server's URL so verifyModuleReferencedImagesAnonymouslyPullable
// can be exercised through the real anonymousPullProbeFunc chain -- token
// exchange, then manifest GET -- rather than a fake bool/error stand-in, so
// these tests reproduce the 1.0.219 failure end to end instead of just at the
// unit level.
func realProbeAt(server *httptest.Server) anonymousPullProbeFunc {
	return func(ctx context.Context, client *http.Client, repoPath, tag string) (bool, error) {
		return probeAnonymousManifestPullAt(ctx, server.Client(), repoPath, tag, server.URL, server.URL)
	}
}

// This is the 1.0.219 regression itself: ghcr refuses the anonymous token
// request for a known-private package with 401. That refusal must be
// reported and the release must proceed, because the baseline already
// records this image as known not anonymously pullable.
func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselinedTokenRequestRefused(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.219", "1.0.219")
	var logBuf strings.Builder
	ctx := Context{Logger: NewLogger(0).WithTraceSink(&logBuf)}

	stub := &anonymousManifestStub{TokenStatus: http.StatusUnauthorized}
	server := stub.start(t)

	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, realProbeAt(server)); err != nil {
		t.Fatalf("a baselined image's token-request refusal must not fail the release: %v", err)
	}
	if !strings.Contains(logBuf.String(), "Not anonymously pullable (baselined") {
		t.Fatalf("expected the baselined refusal to be reported, got log:\n%s", logBuf.String())
	}
}

// A 401 on the token request for an image that is NOT baselined is a
// conclusive "this is private", and a fresh, unrecorded private image must
// still fail the release -- the baseline is an explicit opt-in, never
// inferred from a single probe result.
func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenNotBaselinedTokenRequestRefused(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := Context{Logger: NewLogger(0)}

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

// A probe that cannot reach the registry at all is genuinely inconclusive.
// For an image with no recorded baseline entry, that must still fail the
// release, and the message must say the probe could not run -- not repeat
// the "this image is private" wording a conclusive refusal gets, since
// nothing here established that.
func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenNotBaselinedProbeUnreachable(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := Context{Logger: NewLogger(0)}

	unreachable := "http://127.0.0.1:1"
	probe := func(ctx context.Context, _ *http.Client, repoPath, tag string) (bool, error) {
		return probeAnonymousManifestPullAt(ctx, &http.Client{Timeout: time.Second}, repoPath, tag, unreachable, unreachable)
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

// An unreachable probe for an already-baselined image adds no information --
// the image's status is already recorded as known-not-anonymously-pullable --
// so it must not fail the release either.
func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselinedAndProbeUnreachable(t *testing.T) {
	withAnonymousPullabilityBaseline(t, map[string]bool{"erun-baselined-webhook": true})
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-baselined-webhook")
	execution := fakeExecutionForImage("erun-baselined-webhook", "ghcr.io/sophium/erun-baselined-webhook:1.0.219", "1.0.219")
	var logBuf strings.Builder
	ctx := Context{Logger: NewLogger(0).WithTraceSink(&logBuf)}

	unreachable := "http://127.0.0.1:1"
	probe := func(ctx context.Context, _ *http.Client, repoPath, tag string) (bool, error) {
		return probeAnonymousManifestPullAt(ctx, &http.Client{Timeout: time.Second}, repoPath, tag, unreachable, unreachable)
	}

	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("a baselined image must not fail the release when the probe cannot even run: %v", err)
	}
	if !strings.Contains(logBuf.String(), "could not run") {
		t.Fatalf("expected the unreachable probe to be reported, got log:\n%s", logBuf.String())
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableSkipsProbeInDryRun(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-example-webhook")
	execution := fakeExecutionForImage("erun-example-webhook", "ghcr.io/sophium/erun-example-webhook:1.2.3", "1.2.3")
	ctx := Context{DryRun: true, Logger: NewLogger(0)}

	probe := func(context.Context, *http.Client, string, string) (bool, error) {
		t.Fatal("dry run must not invoke the probe")
		return false, nil
	}
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, execution, probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyModuleReferencedImagesAnonymouslyPullableFailsWhenUnpublished(t *testing.T) {
	root := t.TempDir()
	writeModuleFixture(t, root, "erun-orphaned-webhook")
	execution := BuildExecutionSpec{}
	ctx := Context{Logger: NewLogger(0)}

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
	ctx := Context{Logger: NewLogger(0)}
	probe := func(context.Context, *http.Client, string, string) (bool, error) {
		t.Fatal("must not probe when nothing is referenced")
		return false, nil
	}
	if err := verifyModuleReferencedImagesAnonymouslyPullable(ctx, root, BuildExecutionSpec{}, probe); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestAnonymousPullabilityBaselineIsCurrent(t *testing.T) {
	root := repoRootForDockerignoreTest(t)
	names, err := discoverModuleReferencedImageNames(filepath.Join(root, "erun-devops", "terraform-erun"))
	if err != nil {
		t.Fatalf("scan terraform modules: %v", err)
	}
	referenced := make(map[string]bool, len(names))
	for _, name := range names {
		referenced[name] = true
	}
	for name := range anonymousPullabilityBaseline {
		if !referenced[name] {
			t.Errorf("anonymousPullabilityBaseline names %q, but no terraform module references it any more -- remove the stale entry", name)
		}
	}
}
