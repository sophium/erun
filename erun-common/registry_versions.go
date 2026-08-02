package eruncommon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const DefaultRuntimeImageName = "erun-devops"

// ErrRegistryAuthRequired marks a version listing that failed because the caller
// is not authorized to read the (private) repository, so callers can tell
// "log in to see this image" apart from a genuine registry/network failure.
var ErrRegistryAuthRequired = errors.New("registry authentication required")

// registryStatusError wraps a non-2xx registry response, tagging 401/403 with
// ErrRegistryAuthRequired so callers can surface an actionable "authenticate" hint.
func registryStatusError(kind, status string, code int) error {
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return fmt.Errorf("%s failed: %s: %w", kind, status, ErrRegistryAuthRequired)
	}
	return fmt.Errorf("%s failed: %s", kind, status)
}

// isTransientRegistryError reports whether err is a momentary transport failure
// (connection reset, DNS blip) worth retrying. A timeout is terminal: it means a
// slow or hung registry, and retrying only multiplies the wait a blocking caller
// (erun version) already paid. Auth failures, a cancelled context, and definitive
// HTTP status responses are terminal too — a listing that reached the registry and
// got an answer surfaces as an actionable notice rather than being retried.
func isTransientRegistryError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrRegistryAuthRequired) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return !netErr.Timeout()
	}
	return false
}

type RuntimeRegistryVersions struct {
	Image          string
	Tags           []string
	LatestStable   string
	LatestSnapshot string
}

type RuntimeRegistryVersionResolverFunc func(context.Context) (RuntimeRegistryVersions, error)

type RuntimeVersionSuggestion struct {
	Label   string `json:"label"`
	Version string `json:"version"`
	Source  string `json:"source,omitempty"`
	Image   string `json:"image,omitempty"`
}

const (
	defaultDockerHubRegistryBaseURL = "https://hub.docker.com"
	defaultGHCRRegistryBaseURL      = "https://ghcr.io"
)

// Resolved returns a fully-specified config, applying registry defaults to any unset field.
func (c RuntimeRegistryConfig) Resolved() RuntimeRegistryConfig {
	resolved := RuntimeRegistryConfig{
		Namespace:  strings.TrimSpace(c.Namespace),
		Repository: strings.TrimSpace(c.Repository),
		BaseURL:    strings.TrimSpace(c.BaseURL),
		TokenURL:   strings.TrimSpace(c.TokenURL),
	}
	if resolved.Namespace == "" {
		resolved.Namespace = DefaultContainerRegistry
	}
	if resolved.Repository == "" {
		resolved.Repository = DefaultRuntimeImageName
	}
	if resolved.BaseURL == "" {
		if _, ok := ghcrOwnerFromNamespace(resolved.Namespace); ok {
			resolved.BaseURL = defaultGHCRRegistryBaseURL
		} else {
			resolved.BaseURL = defaultDockerHubRegistryBaseURL
		}
	}
	if resolved.TokenURL == "" {
		resolved.TokenURL = defaultGHCRRegistryBaseURL
	}
	return resolved
}

func ResolveDefaultRuntimeRegistryVersions(ctx context.Context) (RuntimeRegistryVersions, error) {
	config, _, _ := LoadERunConfig()
	return ResolveConfiguredRuntimeRegistryVersions(ctx, config.RuntimeRegistry)
}

// registryListMaxAttempts bounds retries of a transient transport failure;
// registryListRetryBase is the linear backoff step (a var so tests can zero it).
// A few fast attempts clear a momentary network blip without making a real outage
// crawl — a persistent failure still surfaces as the picker's authenticate/unreachable
// notice rather than silently degrading to the anonymous/upstream fallback.
const registryListMaxAttempts = 3

var registryListRetryBase = 200 * time.Millisecond

func ResolveConfiguredRuntimeRegistryVersions(ctx context.Context, cfg RuntimeRegistryConfig) (RuntimeRegistryVersions, error) {
	resolved := cfg.Resolved()
	return resolveRegistryVersionsWithRetry(ctx, func() (RuntimeRegistryVersions, error) {
		return resolveConfiguredRuntimeRegistryVersionsOnce(ctx, resolved)
	})
}

func resolveConfiguredRuntimeRegistryVersionsOnce(ctx context.Context, resolved RuntimeRegistryConfig) (RuntimeRegistryVersions, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	if owner, ok := ghcrOwnerFromNamespace(resolved.Namespace); ok {
		return resolveGHCRRuntimeRegistryVersionsAt(ctx, client, owner, resolved.Repository, resolved.BaseURL, resolved.TokenURL)
	}
	return resolveDockerHubRuntimeRegistryVersionsAt(ctx, client, resolved.Namespace, resolved.Repository, resolved.BaseURL)
}

// resolveRegistryVersionsWithRetry re-runs resolve on a transient transport
// failure with a short linear backoff, so a momentary network blip does not
// immediately degrade to anonymous/upstream access. Auth failures, timeouts, and
// any definitive HTTP status response return on the first try.
func resolveRegistryVersionsWithRetry(ctx context.Context, resolve func() (RuntimeRegistryVersions, error)) (RuntimeRegistryVersions, error) {
	var lastErr error
	for attempt := 0; attempt < registryListMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, registryListRetryBase*time.Duration(attempt)); err != nil {
				return RuntimeRegistryVersions{}, err
			}
		}
		versions, err := resolve()
		if err == nil || !isTransientRegistryError(err) {
			return versions, err
		}
		lastErr = err
	}
	return RuntimeRegistryVersions{}, lastErr
}

func sleepWithContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	if ctx == nil {
		<-timer.C
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ResolveRuntimeImageRegistryVersions is a compatibility overload for callers that pass an explicit namespace/repository pair.
func ResolveRuntimeImageRegistryVersions(ctx context.Context, namespace, repository string) (RuntimeRegistryVersions, error) {
	return ResolveConfiguredRuntimeRegistryVersions(ctx, RuntimeRegistryConfig{
		Namespace:  namespace,
		Repository: repository,
	})
}

func ghcrOwnerFromNamespace(namespace string) (string, bool) {
	trimmed := strings.TrimSpace(namespace)
	if trimmed == "" {
		return "", false
	}
	const prefix = "ghcr.io"
	if !strings.HasPrefix(trimmed, prefix) {
		return "", false
	}
	rest := strings.TrimPrefix(trimmed, prefix)
	rest = strings.TrimPrefix(rest, "/")
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", false
	}
	return rest, true
}

func resolveDockerHubRuntimeRegistryVersionsAt(ctx context.Context, client *http.Client, namespace, repository, baseURL string) (RuntimeRegistryVersions, error) {
	namespace = strings.TrimSpace(namespace)
	repository = strings.TrimSpace(repository)
	if namespace == "" || repository == "" {
		return RuntimeRegistryVersions{}, fmt.Errorf("docker hub namespace and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = defaultDockerHubRegistryBaseURL
	}

	endpoint := baseURL + "/v2/repositories/" + url.PathEscape(namespace) + "/" + url.PathEscape(repository) + "/tags?page_size=100"
	tags := make([]string, 0, 128)
	for endpoint != "" {
		page, err := fetchDockerHubTagPage(ctx, client, endpoint)
		if err != nil {
			return RuntimeRegistryVersions{}, err
		}
		for _, tag := range page.Results {
			if name := strings.TrimSpace(tag.Name); name != "" {
				tags = append(tags, name)
			}
		}
		endpoint = strings.TrimSpace(page.Next)
	}

	versions := latestRuntimeVersionsFromTags(tags)
	versions.Image = namespace + "/" + repository
	return versions, nil
}

func resolveGHCRRuntimeRegistryVersionsAt(ctx context.Context, client *http.Client, owner, repository, baseURL, tokenURL string) (RuntimeRegistryVersions, error) {
	owner = strings.TrimSpace(owner)
	repository = strings.TrimSpace(repository)
	if owner == "" || repository == "" {
		return RuntimeRegistryVersions{}, fmt.Errorf("ghcr owner and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	baseURL = normalizeGHCRBaseURL(baseURL)
	tokenURL = normalizeGHCRBaseURL(tokenURL)

	repoPath := strings.ToLower(escapeRegistryPathSegments(owner) + "/" + escapeRegistryPathSegments(repository))
	// Authenticate with the credential docker pulls with (falling back to gh) so
	// private tenant images list their tags instead of failing anonymously.
	auth, hasAuth := resolveGHCRBasicAuth(owner)
	token, err := fetchGHCRPullToken(ctx, client, repoPath, tokenURL, auth, hasAuth)
	if err != nil {
		return RuntimeRegistryVersions{}, err
	}

	tags, err := collectGHCRTags(ctx, client, baseURL+"/v2/"+repoPath+"/tags/list", token)
	if err != nil {
		return RuntimeRegistryVersions{}, err
	}

	versions := latestRuntimeVersionsFromTags(tags)
	versions.Image = "ghcr.io/" + strings.ToLower(owner+"/"+repository)
	return versions, nil
}

// escapeRegistryPathSegments percent-escapes each slash-separated segment of a
// registry repo path while preserving the slashes, so a multi-segment path like
// charts/erun-backend-postgres addresses /v2/<owner>/charts/erun-backend-postgres
// instead of collapsing the slash into %2F. A single-segment path (the runtime
// image repo) is escaped identically to url.PathEscape.
func escapeRegistryPathSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func normalizeGHCRBaseURL(rawURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return defaultGHCRRegistryBaseURL
	}
	return trimmed
}

func collectGHCRTags(ctx context.Context, client *http.Client, endpoint, token string) ([]string, error) {
	tags := make([]string, 0, 128)
	for endpoint != "" {
		page, next, err := fetchGHCRTagPage(ctx, client, endpoint, token)
		if err != nil {
			return nil, err
		}
		for _, tag := range page.Tags {
			if name := strings.TrimSpace(tag); name != "" {
				tags = append(tags, name)
			}
		}
		endpoint = next
	}
	return tags, nil
}

func fetchGHCRPullToken(ctx context.Context, client *http.Client, repoPath, tokenBaseURL string, auth registryBasicAuth, hasAuth bool) (string, error) {
	tokenBaseURL = strings.TrimRight(strings.TrimSpace(tokenBaseURL), "/")
	if tokenBaseURL == "" {
		tokenBaseURL = defaultGHCRRegistryBaseURL
	}
	tokenURL := tokenBaseURL + "/token?service=ghcr.io&scope=repository:" + repoPath + ":pull"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	// Basic auth on the token exchange makes GHCR mint a bearer token scoped with
	// pull access to a private repo; without it the token grants anonymous access only.
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
		return "", registryStatusError("ghcr token request", resp.Status, resp.StatusCode)
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

type ghcrTagPage struct {
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

func fetchGHCRTagPage(ctx context.Context, client *http.Client, endpoint, token string) (ghcrTagPage, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ghcrTagPage{}, "", err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return ghcrTagPage{}, "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ghcrTagPage{}, "", registryStatusError("ghcr tags request", resp.Status, resp.StatusCode)
	}

	var page ghcrTagPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return ghcrTagPage{}, "", err
	}
	return page, nextLinkFromHeader(resp, endpoint), nil
}

func nextLinkFromHeader(resp *http.Response, baseEndpoint string) string {
	link := resp.Header.Get("Link")
	if link == "" {
		return ""
	}
	for _, segment := range strings.Split(link, ",") {
		segment = strings.TrimSpace(segment)
		if !strings.Contains(segment, `rel="next"`) {
			continue
		}
		target := nextLinkTargetFromSegment(segment)
		if target == "" {
			continue
		}
		return resolveNextLinkTarget(target, baseEndpoint)
	}
	return ""
}

func nextLinkTargetFromSegment(segment string) string {
	start := strings.Index(segment, "<")
	end := strings.Index(segment, ">")
	if start < 0 || end < 0 || end <= start+1 {
		return ""
	}
	return strings.TrimSpace(segment[start+1 : end])
}

func resolveNextLinkTarget(target, baseEndpoint string) string {
	if strings.HasPrefix(target, "/") {
		base, err := url.Parse(baseEndpoint)
		if err == nil {
			ref, err := url.Parse(target)
			if err == nil {
				return base.ResolveReference(ref).String()
			}
		}
	}
	return target
}

type dockerHubTagPage struct {
	Next    string              `json:"next"`
	Results []dockerHubTagEntry `json:"results"`
}

type dockerHubTagEntry struct {
	Name string `json:"name"`
}

func fetchDockerHubTagPage(ctx context.Context, client *http.Client, endpoint string) (dockerHubTagPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return dockerHubTagPage{}, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return dockerHubTagPage{}, err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return dockerHubTagPage{}, registryStatusError("docker hub tags request", resp.Status, resp.StatusCode)
	}

	var page dockerHubTagPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return dockerHubTagPage{}, err
	}
	return page, nil
}

func latestRuntimeVersionsFromTags(tags []string) RuntimeRegistryVersions {
	var latestStable semver
	latestSnapshot := ""
	latestSnapshotTime := ""
	latestStableSet := false
	uniqueTags := make([]string, 0, len(tags))

	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag == "" {
			continue
		}
		uniqueTags = appendUniqueRuntimeTag(uniqueTags, tag)
		if version, ok := newerRegistryStableVersion(tag, latestStable, latestStableSet); ok {
			latestStable, latestStableSet = version, true
		}
		if snapshot, ok := newerRegistrySnapshotVersion(tag, latestSnapshotTime); ok {
			latestSnapshot, latestSnapshotTime = tag, snapshot
		}
	}

	result := RuntimeRegistryVersions{
		Tags:           uniqueTags,
		LatestSnapshot: latestSnapshot,
	}
	if latestStableSet {
		result.LatestStable = formatSemver(latestStable)
	}
	return result
}

func appendUniqueRuntimeTag(tags []string, tag string) []string {
	if slices.Contains(tags, tag) {
		return tags
	}
	return append(tags, tag)
}

func newerRegistryStableVersion(tag string, latest semver, latestSet bool) (semver, bool) {
	version, ok := parseRegistryStableVersion(tag)
	if !ok || latestSet && compareSemver(version, latest) <= 0 {
		return semver{}, false
	}
	return version, true
}

func newerRegistrySnapshotVersion(tag, latestSnapshotTime string) (string, bool) {
	snapshotTime, ok := parseRegistrySnapshotTime(tag)
	if !ok || latestSnapshotTime != "" && snapshotTime <= latestSnapshotTime {
		return "", false
	}
	return snapshotTime, true
}

func (versions RuntimeRegistryVersions) HasVersion(version string) bool {
	version = strings.TrimSpace(version)
	if version == "" || len(versions.Tags) == 0 {
		return false
	}
	return slices.Contains(versions.Tags, version)
}

func RuntimeDeployVersionSuggestions(info BuildInfo, registry RuntimeRegistryVersions) []RuntimeVersionSuggestion {
	info = NormalizeBuildInfo(info)
	suggestions := make([]RuntimeVersionSuggestion, 0, 4)
	addSuggestion := func(label, value string) {
		value = strings.TrimSpace(value)
		if !registry.HasVersion(value) {
			return
		}
		for _, existing := range suggestions {
			if existing.Version == value {
				return
			}
		}
		suggestions = append(suggestions, RuntimeVersionSuggestion{
			Label:   strings.TrimSpace(label),
			Version: value,
		})
	}

	current := strings.TrimSpace(info.Version)
	latestStable := strings.TrimSpace(registry.LatestStable)
	stableBase := current
	if latestStable != "" {
		stableBase = latestStable
	}

	addSuggestion("Current", current)
	addSuggestion("Latest stable", latestStable)
	addSuggestion("Previous", previousPatchVersion(stableBase))
	addSuggestion("Last snapshot", registry.LatestSnapshot)
	return suggestions
}

type semver struct {
	major int
	minor int
	patch int
}

func parseRegistryStableVersion(version string) (semver, bool) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != 3 {
		return semver{}, false
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" {
			return semver{}, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return semver{}, false
		}
		values[index] = value
	}
	return semver{major: values[0], minor: values[1], patch: values[2]}, true
}

func parseRegistrySnapshotTime(version string) (string, bool) {
	_, timestamp, ok := strings.Cut(strings.TrimSpace(version), "-snapshot-")
	if !ok || len(timestamp) != len(localSnapshotTimestampFormat) {
		return "", false
	}
	for _, char := range timestamp {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	return timestamp, true
}

func compareSemver(a, b semver) int {
	switch {
	case a.major != b.major:
		return a.major - b.major
	case a.minor != b.minor:
		return a.minor - b.minor
	default:
		return a.patch - b.patch
	}
}

func formatSemver(version semver) string {
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
}

func previousPatchVersion(version string) string {
	parsed, ok := parseRegistryStableVersion(version)
	if !ok || parsed.patch == 0 {
		return ""
	}
	parsed.patch--
	return formatSemver(parsed)
}
