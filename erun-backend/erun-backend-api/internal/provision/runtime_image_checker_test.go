package provision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const (
	tenantImage    = "ghcr.io/acme/acme-devops:1.2.3"
	canonicalImage = "ghcr.io/acme/erun-devops:1.2.3"
)

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

type staticCredentials struct {
	host       string
	credential RegistryCredential
}

func (c staticCredentials) For(_ context.Context, host string) (RegistryCredential, bool) {
	if c.host != host {
		return RegistryCredential{}, false
	}
	return c.credential, true
}

// ghcrStub is a fake ghcr.io that refuses anonymous pull tokens for private repos.
type ghcrStub struct {
	// manifests maps `<repo>:<tag>` to the status a readable request gets.
	manifests  map[string]int
	private    map[string]bool
	credential RegistryCredential

	tokenRequests []tokenRequest
}

type tokenRequest struct {
	scope         string
	authenticated bool
}

func (s *ghcrStub) start(t *testing.T) {
	t.Helper()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		scope := r.URL.Query().Get("scope")
		username, password, hasAuth := r.BasicAuth()
		authenticated := hasAuth && username == s.credential.Username && password == s.credential.Password
		s.tokenRequests = append(s.tokenRequests, tokenRequest{scope: scope, authenticated: authenticated})
		repo := strings.TrimSuffix(strings.TrimPrefix(scope, "repository:"), ":pull")
		if s.private[repo] && !authenticated {
			// Real ghcr.io issues no anonymous token for private or nonexistent repos.
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"fake-token"}`))
	}))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		trimmed := strings.TrimPrefix(r.URL.Path, "/v2/")
		repo, tag, _ := strings.Cut(trimmed, "/manifests/")
		status, ok := s.manifests[repo+":"+tag]
		if !ok {
			status = http.StatusNotFound
		}
		w.WriteHeader(status)
	}))
	withGHCRTestServers(t, token, api)
}

func assertConfirmedMissing(t *testing.T, checker *GHCRImageChecker, want bool, reason string) {
	t.Helper()
	missing, err := checker.ConfirmedMissing(context.Background(), tenantImage, canonicalImage)
	if err != nil {
		t.Fatalf("ConfirmedMissing: %v", err)
	}
	if missing != want {
		t.Fatalf("confirmedMissing = %v, want %v: %s", missing, want, reason)
	}
}

func TestGHCRImageCheckerPrivateNamespace(t *testing.T) {
	credential := RegistryCredential{Username: "pull-user", Password: "pull-token"}
	newStub := func() *ghcrStub {
		return &ghcrStub{
			manifests:  map[string]int{"acme/erun-devops:1.2.3": http.StatusOK},
			private:    map[string]bool{"acme/acme-devops": true, "acme/erun-devops": true},
			credential: credential,
		}
	}

	t.Run("unauthenticated probe must not confirm absence", func(t *testing.T) {
		newStub().start(t)
		assertConfirmedMissing(t, NewGHCRImageChecker(nil), false,
			"a private namespace's refusal to answer is not proof of absence")
	})

	t.Run("credentialed probe confirms absence", func(t *testing.T) {
		stub := newStub()
		stub.start(t)
		checker := NewGHCRImageChecker(staticCredentials{host: "ghcr.io", credential: credential})
		assertConfirmedMissing(t, checker, true,
			"a credentialed 404 against a namespace the same credential can read is a confirmed absence")
		for _, request := range stub.tokenRequests {
			if !request.authenticated {
				t.Fatalf("token request for scope %q went out unauthenticated", request.scope)
			}
		}
	})

	t.Run("a tenant that publishes its own image keeps it", func(t *testing.T) {
		stub := newStub()
		stub.manifests["acme/acme-devops:1.2.3"] = http.StatusOK
		stub.start(t)
		checker := NewGHCRImageChecker(staticCredentials{host: "ghcr.io", credential: credential})
		assertConfirmedMissing(t, checker, false,
			"an image the registry served must never be overridden")
	})
}

func TestGHCRImageCheckerFailsOpenOnAmbiguousOutcomes(t *testing.T) {
	credential := RegistryCredential{Username: "pull-user", Password: "pull-token"}
	credentials := staticCredentials{host: "ghcr.io", credential: credential}

	t.Run("credential that cannot read the control image", func(t *testing.T) {
		stub := &ghcrStub{
			manifests:  map[string]int{"acme/erun-devops:1.2.3": http.StatusUnauthorized},
			credential: credential,
		}
		stub.start(t)
		assertConfirmedMissing(t, NewGHCRImageChecker(credentials), false,
			"a credential that cannot read a control image that does exist proves nothing")
	})

	t.Run("control image absent at this version", func(t *testing.T) {
		(&ghcrStub{credential: credential}).start(t)
		assertConfirmedMissing(t, NewGHCRImageChecker(credentials), false,
			"the canonical image the bootstrap would run does not resolve either")
	})

	t.Run("token endpoint unreachable", func(t *testing.T) {
		prevToken := ghcrTokenBase
		ghcrTokenBase = "http://127.0.0.1:0"
		defer func() { ghcrTokenBase = prevToken }()
		assertConfirmedMissing(t, &GHCRImageChecker{}, false, "the token endpoint cannot be reached")
	})

	t.Run("non-ghcr host is not this checker's job", func(t *testing.T) {
		missing, err := NewGHCRImageChecker(credentials).ConfirmedMissing(context.Background(),
			"registry.internal.example.com/acme/acme-devops:1.2.3", "registry.internal.example.com/acme/erun-devops:1.2.3")
		if err != nil {
			t.Fatalf("ConfirmedMissing: %v", err)
		}
		if missing {
			t.Fatal("confirmedMissing = true, want false for a non-ghcr.io host")
		}
	})

	t.Run("unparseable references", func(t *testing.T) {
		checker := NewGHCRImageChecker(credentials)
		for name, reference := range map[string][2]string{
			"image":   {"not-a-valid-reference", canonicalImage},
			"control": {tenantImage, "not-a-valid-reference"},
		} {
			missing, err := checker.ConfirmedMissing(context.Background(), reference[0], reference[1])
			if err != nil {
				t.Fatalf("%s: ConfirmedMissing: %v", name, err)
			}
			if missing {
				t.Fatalf("%s: confirmedMissing = true, want false for an unparseable reference", name)
			}
		}
	})
}

// A pull token is per-repository, so each probe must request its own.
func TestGHCRImageCheckerProbesTheRequestedRepositories(t *testing.T) {
	credential := RegistryCredential{Username: "pull-user", Password: "pull-token"}
	stub := &ghcrStub{
		manifests:  map[string]int{"acme/erun-devops:1.2.3": http.StatusOK},
		credential: credential,
	}
	stub.start(t)
	if _, err := NewGHCRImageChecker(staticCredentials{host: "ghcr.io", credential: credential}).
		ConfirmedMissing(context.Background(), tenantImage, canonicalImage); err != nil {
		t.Fatalf("ConfirmedMissing: %v", err)
	}
	scopes := make([]string, 0, len(stub.tokenRequests))
	for _, request := range stub.tokenRequests {
		scopes = append(scopes, request.scope)
	}
	want := []string{"repository:acme/erun-devops:pull", "repository:acme/acme-devops:pull"}
	if len(scopes) != len(want) {
		t.Fatalf("token scopes = %v, want %v", scopes, want)
	}
	for i := range want {
		if scopes[i] != want[i] {
			t.Fatalf("token scopes = %v, want %v", scopes, want)
		}
	}
}

func assertConfirmedPresent(t *testing.T, checker *GHCRImageChecker, want bool, reason string) {
	t.Helper()
	present, err := checker.ConfirmedPresent(context.Background(), tenantImage, canonicalImage)
	if err != nil {
		t.Fatalf("ConfirmedPresent: %v", err)
	}
	if present != want {
		t.Fatalf("confirmedPresent = %v, want %v: %s", present, want, reason)
	}
}

func TestGHCRImageCheckerConfirmedPresent(t *testing.T) {
	credential := RegistryCredential{Username: "pull-user", Password: "pull-token"}
	credentials := staticCredentials{host: "ghcr.io", credential: credential}

	t.Run("a published image is confirmed present", func(t *testing.T) {
		stub := &ghcrStub{
			manifests:  map[string]int{"acme/erun-devops:1.2.3": http.StatusOK, "acme/acme-devops:1.2.3": http.StatusOK},
			credential: credential,
		}
		stub.start(t)
		assertConfirmedPresent(t, NewGHCRImageChecker(credentials), true,
			"a credentialed 200 against a namespace the same credential can read is a confirmed presence")
	})

	t.Run("a missing image is not confirmed present", func(t *testing.T) {
		stub := &ghcrStub{
			manifests:  map[string]int{"acme/erun-devops:1.2.3": http.StatusOK},
			credential: credential,
		}
		stub.start(t)
		assertConfirmedPresent(t, NewGHCRImageChecker(credentials), false,
			"a 404 is not a confirmed presence")
	})

	t.Run("control image absent leaves the question undecided", func(t *testing.T) {
		(&ghcrStub{credential: credential}).start(t)
		assertConfirmedPresent(t, NewGHCRImageChecker(credentials), false,
			"the canonical control image the bootstrap would run does not resolve either")
	})

	t.Run("non-ghcr host is not this checker's job", func(t *testing.T) {
		present, err := NewGHCRImageChecker(credentials).ConfirmedPresent(context.Background(),
			"registry.internal.example.com/acme/acme-devops:1.2.3", "registry.internal.example.com/acme/erun-devops:1.2.3")
		if err != nil {
			t.Fatalf("ConfirmedPresent: %v", err)
		}
		if present {
			t.Fatal("confirmedPresent = true, want false for a non-ghcr.io host")
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
