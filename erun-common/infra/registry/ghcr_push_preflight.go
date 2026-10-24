package registry

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// A bad push credential would otherwise surface after the full multi-arch build,
// with an interactive remedy unusable in detached runs; GitHub reports token
// scopes on any API response, so one request answers it up front.

const githubOAuthScopesHeader = "X-OAuth-Scopes"

// ghcrPushScope is the scope denials are about; read:packages comes with repo access.
const ghcrPushScope = "write:packages"

// MissingGHCRPushScopeError means the push credential is known to lack write:packages.
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

// VerifyGHCRPushScope checks the gh token can publish to a ghcr tag. Inconclusive
// checks return nil so the build fails at the push as before.
func VerifyGHCRPushScope(ctx context.Context, client *http.Client, tag string) error {
	registry := DockerRegistryFromImageTag(tag)
	if !IsGHCRRegistry(registry) {
		return nil
	}
	token, ok := ResolveGHCRTokenViaGH(DockerNamespaceFromTag(tag))
	if !ok {
		return nil
	}
	return verifyGHCRPushScopeFor(ctx, client, registry, token, GithubAPIBaseURL)
}

// GithubAPIBaseURL is the GitHub REST base; overridable so tests can drive the real wire path.
var GithubAPIBaseURL = resolveGitHubAPIBaseURL()

// gitHubAPIBaseURLOverrideEnv is a test seam only; nothing erun ships sets it.
const gitHubAPIBaseURLOverrideEnv = "ERUN_GITHUB_API_BASE_URL_OVERRIDE"

func resolveGitHubAPIBaseURL() string {
	override := strings.TrimSpace(os.Getenv(gitHubAPIBaseURLOverrideEnv))
	if override == "" {
		return "https://api.github.com/"
	}
	if !strings.HasSuffix(override, "/") {
		override += "/"
	}
	return override
}

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

// githubTokenScopes reads a token's scopes with one request to the API root.
func githubTokenScopes(ctx context.Context, client *http.Client, token, apiBaseURL string) ([]string, bool) {
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if strings.TrimSpace(apiBaseURL) == "" {
		apiBaseURL = GithubAPIBaseURL
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

// parseGitHubScopesHeader declines to answer without the header: fine-grained
// PATs and App tokens report no OAuth scopes.
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
