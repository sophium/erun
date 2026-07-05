package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// ghcrStub emulates the GHCR token exchange + tags list. A request carrying HTTP
// Basic auth on /token mints a "scoped" bearer that can read tags; anonymous
// requests mint "anon", which the tags endpoint rejects unless allowPublic.
func ghcrStub(t *testing.T, tags []string, allowPublic bool) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		token := "anon"
		if _, _, ok := r.BasicAuth(); ok {
			token = "scoped"
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"token": token})
	})
	mux.HandleFunc("/v2/", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		authorized := auth == "Bearer scoped" || (allowPublic && auth == "Bearer anon")
		if !authorized {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"tags": tags})
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestResolveGHCRVersionsAuthenticatedPrivate(t *testing.T) {
	server := ghcrStub(t, []string{"1.0.0", "1.0.1", "1.0.2"}, false)
	dir := writeDockerConfig(t, fmt.Sprintf(`{"auths":{"ghcr.io":{"auth":%q}}}`, b64Auth("sophium:tok")))
	useDockerConfigDir(t, dir)

	versions, err := resolveGHCRRuntimeRegistryVersionsAt(context.Background(), server.Client(), "sophium", "frs-devops", server.URL, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if versions.LatestStable != "1.0.2" || len(versions.Tags) != 3 {
		t.Fatalf("got latestStable=%q tags=%d, want 1.0.2/3", versions.LatestStable, len(versions.Tags))
	}
}

func TestResolveGHCRVersionsPrivateWithoutCredIsAuthError(t *testing.T) {
	server := ghcrStub(t, []string{"1.0.0"}, false)
	useDockerConfigDir(t, t.TempDir()) // no docker cred
	useGHToken(t, func(string) (string, bool) { return "", false })

	_, err := resolveGHCRRuntimeRegistryVersionsAt(context.Background(), server.Client(), "sophium", "frs-devops", server.URL, server.URL)
	if !errors.Is(err, ErrRegistryAuthRequired) {
		t.Fatalf("err = %v, want ErrRegistryAuthRequired", err)
	}
}

func TestResolveGHCRVersionsPublicAnonymousUnchanged(t *testing.T) {
	server := ghcrStub(t, []string{"1.0.0", "1.0.5", "1.0.3"}, true)
	useDockerConfigDir(t, t.TempDir()) // no cred → anonymous
	useGHToken(t, func(string) (string, bool) { return "", false })

	versions, err := resolveGHCRRuntimeRegistryVersionsAt(context.Background(), server.Client(), "sophium", "erun-devops", server.URL, server.URL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if versions.LatestStable != "1.0.5" {
		t.Fatalf("latestStable = %q, want 1.0.5", versions.LatestStable)
	}
}

func TestRegistryStatusErrorClassification(t *testing.T) {
	for _, code := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		if err := registryStatusError("x", "s", code); !errors.Is(err, ErrRegistryAuthRequired) {
			t.Fatalf("code %d: want ErrRegistryAuthRequired, got %v", code, err)
		}
	}
	if err := registryStatusError("x", "s", http.StatusInternalServerError); errors.Is(err, ErrRegistryAuthRequired) {
		t.Fatal("500 must not classify as auth-required")
	}
}
