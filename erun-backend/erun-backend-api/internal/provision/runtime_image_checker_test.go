package provision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// withGHCRTestServers points the checker at local httptest servers instead of
// the real ghcr.io, restoring the real hosts afterward.
func withGHCRTestServers(t *testing.T, tokenServer, apiServer *httptest.Server) {
	t.Helper()
	prevAPI, prevToken := ghcrAPIBase, ghcrTokenBase
	ghcrAPIBase = apiServer.URL
	ghcrTokenBase = tokenServer.URL
	t.Cleanup(func() {
		ghcrAPIBase, ghcrTokenBase = prevAPI, prevToken
		apiServer.Close()
		tokenServer.Close()
	})
}

func TestGHCRImageCheckerReportsConfirmedMissing(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fake-token"}`))
	}))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	withGHCRTestServers(t, token, api)

	checker := NewGHCRImageChecker()
	exists, err := checker.Exists(context.Background(), "ghcr.io/acme/acme-devops:1.2.3")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if exists {
		t.Fatal("exists = true, want false on a confirmed 404")
	}
}

func TestGHCRImageCheckerReportsExistingImage(t *testing.T) {
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fake-token"}`))
	}))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	withGHCRTestServers(t, token, api)

	checker := NewGHCRImageChecker()
	exists, err := checker.Exists(context.Background(), "ghcr.io/acme/acme-devops:1.2.3")
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	if !exists {
		t.Fatal("exists = false, want true on a 200 manifest response")
	}
}

// TestGHCRImageCheckerFailsOpenOnAmbiguousOutcomes locks the deliberate
// fail-open behavior: this check exists to catch a *knowable* missing image,
// never to gate a deploy on an inconclusive registry probe.
func TestGHCRImageCheckerFailsOpenOnAmbiguousOutcomes(t *testing.T) {
	t.Run("token endpoint unreachable", func(t *testing.T) {
		checker := &GHCRImageChecker{}
		prevToken := ghcrTokenBase
		ghcrTokenBase = "http://127.0.0.1:0"
		defer func() { ghcrTokenBase = prevToken }()
		exists, err := checker.Exists(context.Background(), "ghcr.io/acme/acme-devops:1.2.3")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Fatal("exists = false, want true (fail open) when the token endpoint cannot be reached")
		}
	})

	t.Run("non-ghcr host is not this checker's job", func(t *testing.T) {
		checker := NewGHCRImageChecker()
		exists, err := checker.Exists(context.Background(), "registry.internal.example.com/acme/acme-devops:1.2.3")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Fatal("exists = false, want true for a non-ghcr.io host")
		}
	})

	t.Run("unparseable image reference", func(t *testing.T) {
		checker := NewGHCRImageChecker()
		exists, err := checker.Exists(context.Background(), "not-a-valid-reference")
		if err != nil {
			t.Fatalf("Exists: %v", err)
		}
		if !exists {
			t.Fatal("exists = false, want true for an unparseable reference")
		}
	})
}

func TestParseImageReference(t *testing.T) {
	host, repo, tag, ok := parseImageReference("ghcr.io/acme/acme-devops:1.2.3")
	if !ok || host != "ghcr.io" || repo != "acme/acme-devops" || tag != "1.2.3" {
		t.Fatalf("parseImageReference = (%q, %q, %q, %v)", host, repo, tag, ok)
	}
	if _, _, _, ok := parseImageReference("no-slash-or-tag"); ok {
		t.Fatal("expected ok=false for a reference with no host segment")
	}
	if _, _, _, ok := parseImageReference("ghcr.io/acme/acme-devops"); ok {
		t.Fatal("expected ok=false for a reference with no tag")
	}
}
