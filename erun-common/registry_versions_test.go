package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
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
	// A definitive HTTP status (even 5xx/429) is terminal for retry purposes: the
	// registry answered, so a retry re-asks an already-answered question.
	if isTransientRegistryError(registryStatusError("x", "s", http.StatusTooManyRequests)) {
		t.Fatal("a 429 status response must not be retried")
	}
}

func withZeroRegistryRetryBackoff(t *testing.T) {
	t.Helper()
	prev := registryListRetryBase
	registryListRetryBase = 0
	t.Cleanup(func() { registryListRetryBase = prev })
}

// fakeNetError models a transport-level failure; timeout distinguishes a slow/hung
// registry (terminal) from a momentary blip (retryable).
type fakeNetError struct{ timeout bool }

func (fakeNetError) Error() string   { return "fake network error" }
func (e fakeNetError) Timeout() bool { return e.timeout }
func (fakeNetError) Temporary() bool { return true }

func TestResolveRegistryVersionsRetriesTransportBlipThenSucceeds(t *testing.T) {
	withZeroRegistryRetryBackoff(t)
	calls := 0
	got, err := resolveRegistryVersionsWithRetry(context.Background(), func() (RuntimeRegistryVersions, error) {
		calls++
		if calls < 2 {
			return RuntimeRegistryVersions{}, fakeNetError{} // momentary transport blip
		}
		return RuntimeRegistryVersions{LatestStable: "1.0.18"}, nil
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got.LatestStable != "1.0.18" {
		t.Fatalf("LatestStable = %q, want 1.0.18", got.LatestStable)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want 2 (one retry)", calls)
	}
}

func TestResolveRegistryVersionsDoesNotRetryAuth(t *testing.T) {
	withZeroRegistryRetryBackoff(t)
	calls := 0
	_, err := resolveRegistryVersionsWithRetry(context.Background(), func() (RuntimeRegistryVersions, error) {
		calls++
		return RuntimeRegistryVersions{}, fmt.Errorf("no: %w", ErrRegistryAuthRequired)
	})
	if !errors.Is(err, ErrRegistryAuthRequired) {
		t.Fatalf("err = %v, want auth", err)
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (auth must not retry)", calls)
	}
}

func TestResolveRegistryVersionsDoesNotRetryTimeout(t *testing.T) {
	withZeroRegistryRetryBackoff(t)
	calls := 0
	_, err := resolveRegistryVersionsWithRetry(context.Background(), func() (RuntimeRegistryVersions, error) {
		calls++
		return RuntimeRegistryVersions{}, fakeNetError{timeout: true}
	})
	if err == nil {
		t.Fatal("expected the timeout error to surface")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1 (a timeout must not be retried)", calls)
	}
}

func TestResolveRegistryVersionsExhaustsTransportRetries(t *testing.T) {
	withZeroRegistryRetryBackoff(t)
	calls := 0
	_, err := resolveRegistryVersionsWithRetry(context.Background(), func() (RuntimeRegistryVersions, error) {
		calls++
		return RuntimeRegistryVersions{}, fakeNetError{}
	})
	if !isTransientRegistryError(err) {
		t.Fatalf("err = %v, want a transient transport error", err)
	}
	if calls != registryListMaxAttempts {
		t.Fatalf("calls = %d, want %d attempts", calls, registryListMaxAttempts)
	}
}

func TestIsTransientRegistryError(t *testing.T) {
	if isTransientRegistryError(nil) {
		t.Error("nil is not transient")
	}
	if isTransientRegistryError(context.Canceled) {
		t.Error("context.Canceled is not transient")
	}
	if isTransientRegistryError(context.DeadlineExceeded) {
		t.Error("a timeout is terminal, not transient")
	}
	if isTransientRegistryError(fmt.Errorf("%w", ErrRegistryAuthRequired)) {
		t.Error("auth is not transient")
	}
	if !isTransientRegistryError(fakeNetError{}) {
		t.Error("a fast transport error is transient")
	}
	if isTransientRegistryError(fakeNetError{timeout: true}) {
		t.Error("a transport timeout is terminal")
	}
	if !isTransientRegistryError(&url.Error{Op: "Get", URL: "https://ghcr.io", Err: fakeNetError{}}) {
		t.Error("a transport url.Error is transient")
	}
	if isTransientRegistryError(errors.New("json decode failed")) {
		t.Error("a non-network error is terminal")
	}
}

func TestIsRuntimeRegistryVersionTag(t *testing.T) {
	tests := []struct {
		tag  string
		want bool
	}{
		{"1.0.250", true},
		{"1.0.192-snapshot-20260821151853", true},
		{"1.0.250-pr.4fa33ad5-arm64", true},
		{"1.0.250-arm64", true},
		{"1.0.250-amd64", true},
		{"2.0.0-rc.1", true},
		{"permcheck-tmp", false},
		{"authprobe", false},
		{"latest", false},
		{"stable", false},
		{"1.0", false},
		{"1.0.250.1", false},
		{"1.0.250-", false},
		{"v1.0.250", false},
		{"", false},
	}
	for _, test := range tests {
		if got := isRuntimeRegistryVersionTag(test.tag); got != test.want {
			t.Errorf("isRuntimeRegistryVersionTag(%q) = %v, want %v", test.tag, got, test.want)
		}
	}
}

// A stray registry tag must not reach Tags: Tags is what every surface
// enumerates, and the desktop picker maps it into its version select with no
// filter of its own, so an entry here is presented to the operator as a
// pinnable version.
func TestLatestRuntimeVersionsFromTagsOffersOnlyVersionShapedTags(t *testing.T) {
	versions := latestRuntimeVersionsFromTags([]string{
		"1.0.250",
		"1.0.192-snapshot-20260821151853",
		"1.0.250-pr.4fa33ad5-arm64",
		"1.0.250-arm64",
		"permcheck-tmp",
		"authprobe",
	})

	for _, stray := range []string{"permcheck-tmp", "authprobe"} {
		if versions.HasVersion(stray) {
			t.Errorf("%q is not a version, so HasVersion must not accept it", stray)
		}
		for _, offered := range versions.Tags {
			if offered == stray {
				t.Errorf("%q must not appear in Tags; Tags = %v", stray, versions.Tags)
			}
		}
	}

	for _, want := range []string{
		"1.0.250",
		"1.0.192-snapshot-20260821151853",
		"1.0.250-pr.4fa33ad5-arm64",
		"1.0.250-arm64",
	} {
		if !versions.HasVersion(want) {
			t.Errorf("version-shaped tag %q must stay pinnable; Tags = %v", want, versions.Tags)
		}
	}

	if len(versions.Tags) != 4 {
		t.Errorf("Tags = %v, want only the four version-shaped tags", versions.Tags)
	}
	if versions.LatestStable != "1.0.250" {
		t.Errorf("LatestStable = %q, want 1.0.250", versions.LatestStable)
	}
	if versions.LatestSnapshot != "1.0.192-snapshot-20260821151853" {
		t.Errorf("LatestSnapshot = %q, want 1.0.192-snapshot-20260821151853", versions.LatestSnapshot)
	}
}
