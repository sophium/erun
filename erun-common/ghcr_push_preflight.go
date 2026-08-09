package eruncommon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// A release discovers a bad registry credential at the push — after every image
// has been built for every architecture. That is the most expensive possible
// moment to learn it, and the remedy it then offers is an interactive login,
// which is unanswerable in exactly the contexts erun pushes callers toward: a
// detached job, an agent run, any release that is not sitting at a terminal.
//
// The credential is knowable in advance. erun logs docker into ghcr with the gh
// token, and GitHub reports a token's scopes on any API response, so one request
// answers "can this publish?" before the first build starts.

const githubOAuthScopesHeader = "X-OAuth-Scopes"

// ghcrPushScope is the scope a ghcr push requires. read:packages is implied by
// repo access for a private package and is not what denials are about.
const ghcrPushScope = "write:packages"

// MissingGHCRPushScopeError is returned when the credential erun would use is
// known not to permit publishing.
type MissingGHCRPushScopeError struct {
	Registry string
	Scopes   []string
}

func (e *MissingGHCRPushScopeError) Error() string {
	have := "none"
	if len(e.Scopes) > 0 {
		have = strings.Join(e.Scopes, ", ")
	}
	return fmt.Sprintf(
		"the gh token erun publishes with cannot push to %s: it lacks the %s scope (it has: %s).\n"+
			"Fix it before building, with:\n"+
			"  gh auth refresh -h github.com -s %s\n"+
			"Publishing is checked up front because a release that cannot push would otherwise "+
			"fail at the push, after building every image for every architecture.",
		e.Registry, ghcrPushScope, have, ghcrPushScope)
}

// VerifyGHCRPushScope reports whether the gh token can publish to the registry a
// tag names. It answers only for ghcr, and only when it can answer with
// certainty.
//
// An inconclusive check — no gh on PATH, no token, GitHub unreachable, a
// response without the scopes header — returns nil rather than blocking the
// build. The point is to convert a *known* failure into an immediate one, not to
// invent a new way for a release to refuse to start; a credential that is
// actually bad still fails at the push exactly as it does today.
func VerifyGHCRPushScope(ctx context.Context, client *http.Client, tag string) error {
	registry := dockerRegistryFromImageTag(tag)
	if !isGHCRRegistry(registry) {
		return nil
	}
	token, ok := resolveGHCRTokenViaGH(DockerNamespaceFromTag(tag))
	if !ok {
		return nil
	}
	return verifyGHCRPushScopeFor(ctx, client, registry, token, githubAPIBaseURL)
}

// githubAPIBaseURL is where the scope check asks. A variable so the test can
// point it at a server that answers like GitHub does.
var githubAPIBaseURL = "https://api.github.com/"

// verifyGHCRPushScopeFor is the decision, with the token and the endpoint as
// inputs so it can be exercised without a real credential.
func verifyGHCRPushScopeFor(ctx context.Context, client *http.Client, registry, token, apiBaseURL string) error {
	if strings.TrimSpace(token) == "" {
		return nil
	}
	scopes, ok := githubTokenScopes(ctx, client, token, apiBaseURL)
	if !ok {
		return nil
	}
	for _, scope := range scopes {
		if scope == ghcrPushScope {
			return nil
		}
	}
	return &MissingGHCRPushScopeError{Registry: registry, Scopes: scopes}
}

// githubTokenScopes returns what a token is scoped for. GitHub reports it on any
// authenticated response, so this costs one request against the API root and
// never touches a repository.
func githubTokenScopes(ctx context.Context, client *http.Client, token, apiBaseURL string) ([]string, bool) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(apiBaseURL) == "" {
		apiBaseURL = githubAPIBaseURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiBaseURL, nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := client.Do(req)
	if err != nil {
		return nil, false
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	return parseGitHubScopesHeader(resp.Header)
}

// parseGitHubScopesHeader reads the scopes GitHub reports on a response.
//
// A missing header is not an empty scope list: a fine-grained PAT or a GitHub
// App token reports no OAuth scopes at all, which is a different permission
// model rather than an absence of permission, so it declines to answer.
func parseGitHubScopesHeader(header http.Header) ([]string, bool) {
	raw, ok := header[http.CanonicalHeaderKey(githubOAuthScopesHeader)]
	if !ok {
		return nil, false
	}
	scopes := make([]string, 0, 8)
	for _, value := range raw {
		for _, scope := range strings.Split(value, ",") {
			if scope = strings.TrimSpace(scope); scope != "" {
				scopes = append(scopes, scope)
			}
		}
	}
	return scopes, true
}
