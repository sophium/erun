package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ociRegistryHostFromNamespace splits a namespace that names a registry host of
// its own -- a private registry, an ECR account, an in-cluster registry -- into
// that host and any repository prefix below it. A Docker Hub namespace is a bare
// account name with neither a dot nor a port, so it stays on the Hub path.
func ociRegistryHostFromNamespace(namespace string) (string, string, bool) {
	trimmed := strings.Trim(strings.TrimSpace(namespace), "/")
	if trimmed == "" {
		return "", "", false
	}
	host, rest, _ := strings.Cut(trimmed, "/")
	if !strings.Contains(host, ".") && !strings.Contains(host, ":") {
		return "", "", false
	}
	return host, strings.Trim(rest, "/"), true
}

// resolveOCIRuntimeRegistryVersionsAt lists a repository on any registry that
// speaks the OCI distribution API. Without this, every non-ghcr, non-Hub
// registry fell through to the Docker Hub resolver, which then looked up a
// repository named after the registry host and reported the image as
// unreachable -- so a tenant could never see the versions of its own runtime
// image in a private registry.
//
// Auth is the credential docker itself pulls with, so "if docker can pull it,
// the picker lists it" holds for private registries too. Basic is sent up front
// because ECR accepts it directly; a registry that answers with a bearer
// challenge instead gets the token exchange and a retry.
func resolveOCIRuntimeRegistryVersionsAt(ctx context.Context, client *http.Client, host, prefix, repository, baseURL string) (RuntimeRegistryVersions, error) {
	host = strings.TrimSpace(host)
	repository = strings.TrimSpace(repository)
	if host == "" || repository == "" {
		return RuntimeRegistryVersions{}, fmt.Errorf("registry host and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}

	repoPath := escapeRegistryPathSegments(repository)
	if prefix = strings.Trim(strings.TrimSpace(prefix), "/"); prefix != "" {
		repoPath = escapeRegistryPathSegments(prefix) + "/" + repoPath
	}

	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		base = "https://" + host
	}

	auth, hasAuth := resolveOCIRegistryBasicAuth(host)
	tags, err := collectOCITags(ctx, client, base+"/v2/"+repoPath+"/tags/list", repoPath, auth, hasAuth)
	if err != nil {
		return RuntimeRegistryVersions{}, err
	}

	image := host
	if prefix != "" {
		image += "/" + prefix
	}
	versions := latestRuntimeVersionsFromTags(tags)
	versions.Image = image + "/" + repository
	return versions, nil
}

func collectOCITags(ctx context.Context, client *http.Client, endpoint, repoPath string, auth registryBasicAuth, hasAuth bool) ([]string, error) {
	tags := make([]string, 0, 128)
	bearer := ""
	for endpoint != "" {
		page, next, challenge, err := fetchOCITagPage(ctx, client, endpoint, auth, hasAuth, bearer)
		if err != nil {
			return nil, err
		}
		// A registry that wants a bearer token says so once; exchange the same
		// credential for one and re-read this page rather than giving up.
		if challenge != "" && bearer == "" {
			token, tokenErr := fetchOCIPullToken(ctx, client, challenge, repoPath, auth, hasAuth)
			if tokenErr != nil {
				return nil, tokenErr
			}
			bearer = token
			continue
		}
		tags = append(tags, page.Tags...)
		endpoint = next
	}
	return tags, nil
}

func fetchOCITagPage(ctx context.Context, client *http.Client, endpoint string, auth registryBasicAuth, hasAuth bool, bearer string) (ghcrTagPage, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ghcrTagPage{}, "", "", err
	}
	switch {
	case bearer != "":
		req.Header.Set("Authorization", "Bearer "+bearer)
	case hasAuth:
		req.SetBasicAuth(auth.username, auth.secret)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ghcrTagPage{}, "", "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized {
		if challenge := resp.Header.Get("WWW-Authenticate"); strings.HasPrefix(strings.ToLower(strings.TrimSpace(challenge)), "bearer ") {
			return ghcrTagPage{}, "", challenge, nil
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ghcrTagPage{}, "", "", registryStatusError("registry tags request", resp.Status, resp.StatusCode)
	}

	var page ghcrTagPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return ghcrTagPage{}, "", "", err
	}
	return page, nextLinkFromHeader(resp, endpoint), "", nil
}

// fetchOCIPullToken exchanges the registry's bearer challenge for a pull token,
// scoping it to this repository when the challenge omits a scope.
func fetchOCIPullToken(ctx context.Context, client *http.Client, challenge, repoPath string, auth registryBasicAuth, hasAuth bool) (string, error) {
	params := parseBearerChallenge(challenge)
	realm := strings.TrimSpace(params["realm"])
	if realm == "" {
		return "", fmt.Errorf("registry bearer challenge has no realm")
	}
	query := make([]string, 0, 2)
	if service := strings.TrimSpace(params["service"]); service != "" {
		query = append(query, "service="+service)
	}
	scope := strings.TrimSpace(params["scope"])
	if scope == "" {
		scope = "repository:" + repoPath + ":pull"
	}
	query = append(query, "scope="+scope)

	tokenURL := realm + "?" + strings.Join(query, "&")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	if hasAuth {
		req.SetBasicAuth(auth.username, auth.secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", registryStatusError("registry token request", resp.Status, resp.StatusCode)
	}
	var payload struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", err
	}
	if payload.Token != "" {
		return payload.Token, nil
	}
	return payload.AccessToken, nil
}

func parseBearerChallenge(challenge string) map[string]string {
	params := make(map[string]string, 3)
	rest := strings.TrimSpace(challenge)
	if idx := strings.Index(rest, " "); idx >= 0 {
		rest = rest[idx+1:]
	}
	for _, field := range strings.Split(rest, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(field), "=")
		if !ok {
			continue
		}
		params[strings.ToLower(strings.TrimSpace(key))] = strings.Trim(strings.TrimSpace(value), `\"`)
	}
	return params
}
