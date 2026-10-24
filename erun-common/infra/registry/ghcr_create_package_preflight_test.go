package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeGHCRRegistry serves the token exchange and blob-upload start, recording
// abandon DELETEs and the repository each request named.
type fakeGHCRRegistry struct {
	// A non-2xx TokenStatus makes the check inconclusive.
	TokenStatus  int
	UploadStatus int
	UploadBody   string

	mu           sync.Mutex
	repositories []string
	deleted      []string
}

func (r *fakeGHCRRegistry) start(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/token"):
			r.handleToken(w)
		case req.Method == http.MethodPost && strings.Contains(req.URL.Path, "/blobs/uploads/"):
			r.handleUploadStart(w, req)
		case req.Method == http.MethodDelete:
			r.handleDelete(w, req)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func (r *fakeGHCRRegistry) handleToken(w http.ResponseWriter) {
	status := r.TokenStatus
	if status == 0 {
		status = http.StatusOK
	}
	if status < 200 || status >= 300 {
		w.WriteHeader(status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"token":"probe-token"}`))
}

func (r *fakeGHCRRegistry) handleUploadStart(w http.ResponseWriter, req *http.Request) {
	repo := strings.TrimSuffix(strings.TrimPrefix(req.URL.Path, "/v2/"), "/blobs/uploads/")
	r.mu.Lock()
	r.repositories = append(r.repositories, repo)
	r.mu.Unlock()
	status := r.UploadStatus
	if status == 0 {
		status = http.StatusAccepted
	}
	if status == http.StatusAccepted || status == http.StatusCreated {
		w.Header().Set("Location", "/v2/"+repo+"/blobs/uploads/probe-session")
		w.WriteHeader(status)
		return
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte(r.UploadBody))
}

func (r *fakeGHCRRegistry) handleDelete(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.deleted = append(r.deleted, req.URL.Path)
	r.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (r *fakeGHCRRegistry) sawRepository(repo string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, seen := range r.repositories {
		if seen == repo {
			return true
		}
	}
	return false
}

func (r *fakeGHCRRegistry) deletedCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.deleted)
}

// A token that pushes existing packages can still be denied create_package.
func TestANewPackageDeniedByTheRegistryIsRefusedUpFront(t *testing.T) {
	registry := &fakeGHCRRegistry{UploadStatus: http.StatusForbidden, UploadBody: `denied: permission_denied: create_package`}
	server := registry.start(t)

	err := verifyGHCRCanPushRepositoryFor(context.Background(), nil, "ghcr.io", "sophium/erun-zitadel", RegistryBasicAuth{Username: "sophium", Secret: "token"}, server.URL)
	if err == nil {
		t.Fatal("a repository the registry denies push to must be refused before the build")
	}
	var missing *MissingGHCRCreatePackageError
	if !errors.As(err, &missing) {
		t.Fatalf("expected a MissingGHCRCreatePackageError, got %T: %v", err, err)
	}
	message := missing.Error()
	for _, want := range []string{"create_package", "sophium/erun-zitadel", "classic PAT", "write:packages"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the error must be actionable, missing %q in:\n%s", want, message)
		}
	}
	if !registry.sawRepository("sophium/erun-zitadel") {
		t.Fatal("the probe must name the exact repository it checked")
	}
}

func TestAPushableRepositoryIsAccepted(t *testing.T) {
	registry := &fakeGHCRRegistry{UploadStatus: http.StatusAccepted}
	server := registry.start(t)

	if err := verifyGHCRCanPushRepositoryFor(context.Background(), nil, "ghcr.io", "sophium/erun-devops", RegistryBasicAuth{Username: "sophium", Secret: "token"}, server.URL); err != nil {
		t.Fatalf("a repository the registry grants push to must be accepted: %v", err)
	}
	if registry.deletedCount() != 1 {
		t.Fatalf("the probe's own upload session must be abandoned exactly once, got %d deletes", registry.deletedCount())
	}
}

func TestGHCRCreatePackageCheckNeverBlocksOnAnInconclusiveAnswer(t *testing.T) {
	auth := RegistryBasicAuth{Username: "sophium", Secret: "token"}

	t.Run("token exchange fails", func(t *testing.T) {
		registry := &fakeGHCRRegistry{TokenStatus: http.StatusInternalServerError}
		server := registry.start(t)
		if err := verifyGHCRCanPushRepositoryFor(context.Background(), nil, "ghcr.io", "sophium/erun-devops", auth, server.URL); err != nil {
			t.Fatalf("an unresolvable token must not block the build: %v", err)
		}
	})

	t.Run("upload probe returns an unexpected status", func(t *testing.T) {
		registry := &fakeGHCRRegistry{UploadStatus: http.StatusServiceUnavailable}
		server := registry.start(t)
		if err := verifyGHCRCanPushRepositoryFor(context.Background(), nil, "ghcr.io", "sophium/erun-devops", auth, server.URL); err != nil {
			t.Fatalf("an unexpected registry response must not block the build: %v", err)
		}
	})

	t.Run("registry unreachable", func(t *testing.T) {
		if err := verifyGHCRCanPushRepositoryFor(context.Background(), nil, "ghcr.io", "sophium/erun-devops", auth, "http://127.0.0.1:1"); err != nil {
			t.Fatalf("an unreachable registry must not block the build: %v", err)
		}
	})
}

// Neither entry point may reach the network for non-ghcr or without a credential.
func TestGHCREntryPointsSkipWhenNotApplicable(t *testing.T) {
	if err := VerifyGHCRCanPushImage(context.Background(), nil, "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme/api:1.0.0"); err != nil {
		t.Fatalf("a non-ghcr registry must not be probed: %v", err)
	}
	if err := VerifyGHCRCanPushChart(context.Background(), nil, "oci://registry.example.com/sophium/charts", "erun-zitadel"); err != nil {
		t.Fatalf("a non-ghcr OCI repo must not be probed: %v", err)
	}
}

func TestDockerRepositoryFromTag(t *testing.T) {
	cases := map[string]string{
		"ghcr.io/sophium/erun-zitadel:1.0.180":       "sophium/erun-zitadel",
		"ghcr.io/sophium/erun-zitadel:1.0.180-amd64": "sophium/erun-zitadel",
		"erun-zitadel:1.0.180":                       "",
	}
	for tag, want := range cases {
		if got := dockerRepositoryFromTag(tag); got != want {
			t.Errorf("dockerRepositoryFromTag(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestSplitOCIChartRepoAndNamespace(t *testing.T) {
	registry, path := splitOCIChartRepo("oci://ghcr.io/sophium/charts")
	if registry != "ghcr.io" || path != "sophium/charts" {
		t.Fatalf("splitOCIChartRepo = (%q, %q), want (ghcr.io, sophium/charts)", registry, path)
	}
	if namespace := namespaceFromOCIPath(path); namespace != "sophium" {
		t.Fatalf("namespaceFromOCIPath(%q) = %q, want sophium", path, namespace)
	}
}
