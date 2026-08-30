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

func TestProbeAnonymousManifestPullAt(t *testing.T) {
	t.Run("pullable manifest reports true", func(t *testing.T) {
		stub := &anonymousManifestStub{ManifestStatus: http.StatusOK}
		server := stub.start(t)

		pullable, err := probeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !pullable {
			t.Fatal("expected the manifest to report pullable")
		}
	})

	t.Run("denied manifest reports false, not an error", func(t *testing.T) {
		stub := &anonymousManifestStub{ManifestStatus: http.StatusForbidden}
		server := stub.start(t)

		pullable, err := probeAnonymousManifestPullAt(context.Background(), server.Client(), "sophium/erun-example", "1.0.0", server.URL, server.URL)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if pullable {
			t.Fatal("expected the manifest to report not pullable")
		}
	})

	// This is the regression for the defect release-time verification used to
	// have: verifyPublishedReleaseArtifacts re-resolves a manifest through
	// `docker manifest inspect`, which authenticates with whatever the local
	// daemon has stored, so it can never observe whether a stranger with no
	// credential at all could pull the same image. This probe must never send
	// one, in either direction: not on the token exchange, and not folded into
	// the manifest request's own Authorization header.
	t.Run("sends no credentials", func(t *testing.T) {
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
	})
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

func TestVerifyModuleReferencedImagesAnonymouslyPullablePassesWhenBaselined(t *testing.T) {
	root := t.TempDir()
	// erun-dns01-webhook is the real, current anonymousPullabilityBaseline
	// entry, so this exercises the baseline as it stands today, not a
	// fixture-only name.
	writeModuleFixture(t, root, "erun-dns01-webhook")
	execution := fakeExecutionForImage("erun-dns01-webhook", "ghcr.io/sophium/erun-dns01-webhook:1.0.217", "1.0.217")
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
