package registry

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// githubStub returns a token's scopes on a header, like GitHub's API root.
func githubStub(t *testing.T, scopes string, sendHeader bool, status int) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if sendHeader {
			w.Header().Set(githubOAuthScopesHeader, scopes)
		}
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/"
}

// A token that can clone and PR but not publish must fail before the build.
func TestAPushCredentialWithoutWritePackagesIsRefusedUpFront(t *testing.T) {
	api := githubStub(t, "gist, read:org, repo, workflow", true, http.StatusOK)

	err := verifyGHCRPushScopeFor(context.Background(), nil, "ghcr.io", "gho_token", api)
	if err == nil {
		t.Fatal("a token lacking write:packages must be refused before the build")
	}
	var missing *MissingGHCRPushScopeError
	if !asMissingScope(err, &missing) {
		t.Fatalf("expected a MissingGHCRPushScopeError, got %T: %v", err, err)
	}
	message := err.Error()
	for _, want := range []string{"write:packages", "gh auth refresh", "ghcr.io"} {
		if !strings.Contains(message, want) {
			t.Fatalf("the error must tell the operator how to fix it, missing %q in:\n%s", want, message)
		}
	}
	if !strings.Contains(message, "read:org") {
		t.Fatalf("the error should report the scopes the token does carry:\n%s", message)
	}
}

func TestAPushCredentialWithWritePackagesIsAccepted(t *testing.T) {
	api := githubStub(t, "gist, repo, write:packages", true, http.StatusOK)

	if err := verifyGHCRPushScopeFor(context.Background(), nil, "ghcr.io", "gho_token", api); err != nil {
		t.Fatalf("a token carrying write:packages must pass: %v", err)
	}
}

// Anything the check cannot answer with certainty lets the build proceed.
func TestAnInconclusiveCheckNeverBlocksTheBuild(t *testing.T) {
	cases := map[string]string{
		"no token at all":           "",
		"github unreachable":        "http://127.0.0.1:1/",
		"a response without scopes": githubStub(t, "", false, http.StatusOK),
		"an error response":         githubStub(t, "repo", true, http.StatusUnauthorized),
	}
	for name, api := range cases {
		token := "gho_token"
		if name == "no token at all" {
			token = ""
			api = githubStub(t, "repo", true, http.StatusOK)
		}
		if err := verifyGHCRPushScopeFor(context.Background(), nil, "ghcr.io", token, api); err != nil {
			t.Fatalf("%s must not block the build, got %v", name, err)
		}
	}
}

// Fine-grained PATs and App tokens report no OAuth scopes; that is not a denial.
func TestATokenWithADifferentPermissionModelIsNotJudged(t *testing.T) {
	api := githubStub(t, "", false, http.StatusOK)

	if err := verifyGHCRPushScopeFor(context.Background(), nil, "ghcr.io", "github_pat_x", api); err != nil {
		t.Fatalf("a token reporting no OAuth scopes must not be refused, got %v", err)
	}
}

func TestANonGHCRRegistryIsNotChecked(t *testing.T) {
	if err := VerifyGHCRPushScope(context.Background(), nil, "020362606330.dkr.ecr.eu-west-2.amazonaws.com/acme/api:1.0.0"); err != nil {
		t.Fatalf("a non-ghcr registry must not be judged by a gh token, got %v", err)
	}
}

func asMissingScope(err error, target **MissingGHCRPushScopeError) bool {
	return errors.As(err, target)
}
