package registry

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

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// ErrRegistryAuthRequired separates "log in to see this image" from registry or network failures.
var ErrRegistryAuthRequired = errors.New("registry authentication required")

// RegistryStatusError wraps a non-2xx response, tagging 401/403 with ErrRegistryAuthRequired.
func RegistryStatusError(kind, status string, code int) error {
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return fmt.Errorf("%s failed: %s: %w", kind, status, ErrRegistryAuthRequired)
	}
	return fmt.Errorf("%s failed: %s", kind, status)
}

// isTransientRegistryError reports a retryable transport blip. Timeouts, auth
// failures, cancellation and any HTTP status are terminal.
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

type RuntimeVersionSuggestion struct {
	Label   string `json:"label"`
	Version string `json:"version"`
	Source  string `json:"source,omitempty"`
	Image   string `json:"image,omitempty"`
}

func ResolveDefaultRuntimeRegistryVersions(ctx context.Context) (model.RuntimeRegistryVersions, error) {
	config, _, _ := configpkg.LoadERunConfig()
	return ResolveConfiguredRuntimeRegistryVersions(ctx, config.RuntimeRegistry)
}

// registryListRetryBase is a var so tests can zero it.
const registryListMaxAttempts = 3

var registryListRetryBase = 200 * time.Millisecond

func ResolveConfiguredRuntimeRegistryVersions(ctx context.Context, cfg configpkg.RuntimeRegistryConfig) (model.RuntimeRegistryVersions, error) {
	resolved := cfg.Resolved()
	return resolveRegistryVersionsWithRetry(ctx, func() (model.RuntimeRegistryVersions, error) {
		return resolveConfiguredRuntimeRegistryVersionsOnce(ctx, resolved)
	})
}

func resolveConfiguredRuntimeRegistryVersionsOnce(ctx context.Context, resolved configpkg.RuntimeRegistryConfig) (model.RuntimeRegistryVersions, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	if owner, ok := configpkg.GhcrOwnerFromNamespace(resolved.Namespace); ok {
		return resolveGHCRRuntimeRegistryVersionsAt(ctx, client, owner, resolved.Repository, resolved.BaseURL, resolved.TokenURL)
	}
	if host, prefix, ok := configpkg.OciRegistryHostFromNamespace(resolved.Namespace); ok {
		return resolveOCIRuntimeRegistryVersionsAt(ctx, client, host, prefix, resolved.Repository, resolved.BaseURL)
	}
	return resolveDockerHubRuntimeRegistryVersionsAt(ctx, client, resolved.Namespace, resolved.Repository, resolved.BaseURL)
}

func resolveRegistryVersionsWithRetry(ctx context.Context, resolve func() (model.RuntimeRegistryVersions, error)) (model.RuntimeRegistryVersions, error) {
	var lastErr error
	for attempt := 0; attempt < registryListMaxAttempts; attempt++ {
		if attempt > 0 {
			if err := sleepWithContext(ctx, registryListRetryBase*time.Duration(attempt)); err != nil {
				return model.RuntimeRegistryVersions{}, err
			}
		}
		versions, err := resolve()
		if err == nil || !isTransientRegistryError(err) {
			return versions, err
		}
		lastErr = err
	}
	return model.RuntimeRegistryVersions{}, lastErr
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

// ResolveRuntimeImageRegistryVersions takes an explicit namespace/repository pair.
func ResolveRuntimeImageRegistryVersions(ctx context.Context, namespace, repository string) (model.RuntimeRegistryVersions, error) {
	return ResolveConfiguredRuntimeRegistryVersions(ctx, configpkg.RuntimeRegistryConfig{
		Namespace:  namespace,
		Repository: repository,
	})
}

func resolveDockerHubRuntimeRegistryVersionsAt(ctx context.Context, client *http.Client, namespace, repository, baseURL string) (model.RuntimeRegistryVersions, error) {
	namespace = strings.TrimSpace(namespace)
	repository = strings.TrimSpace(repository)
	if namespace == "" || repository == "" {
		return model.RuntimeRegistryVersions{}, fmt.Errorf("docker hub namespace and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		baseURL = configpkg.DefaultDockerHubRegistryBaseURL
	}

	endpoint := baseURL + "/v2/repositories/" + url.PathEscape(namespace) + "/" + url.PathEscape(repository) + "/tags?page_size=100"
	tags := make([]string, 0, 128)
	for endpoint != "" {
		page, err := fetchDockerHubTagPage(ctx, client, endpoint)
		if err != nil {
			return model.RuntimeRegistryVersions{}, err
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

func resolveGHCRRuntimeRegistryVersionsAt(ctx context.Context, client *http.Client, owner, repository, baseURL, tokenURL string) (model.RuntimeRegistryVersions, error) {
	owner = strings.TrimSpace(owner)
	repository = strings.TrimSpace(repository)
	if owner == "" || repository == "" {
		return model.RuntimeRegistryVersions{}, fmt.Errorf("ghcr owner and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}
	baseURL = NormalizeGHCRBaseURL(baseURL)
	tokenURL = NormalizeGHCRBaseURL(tokenURL)

	repoPath := strings.ToLower(escapeRegistryPathSegments(owner) + "/" + escapeRegistryPathSegments(repository))
	// Authenticate so private tenant images list their tags.
	auth, hasAuth := resolveGHCRBasicAuth(owner)
	token, err := FetchGHCRPullToken(ctx, client, repoPath, tokenURL, auth, hasAuth)
	if err != nil {
		return model.RuntimeRegistryVersions{}, err
	}

	tags, err := collectGHCRTags(ctx, client, baseURL+"/v2/"+repoPath+"/tags/list", token)
	if err != nil {
		return model.RuntimeRegistryVersions{}, err
	}

	versions := latestRuntimeVersionsFromTags(tags)
	versions.Image = "ghcr.io/" + strings.ToLower(owner+"/"+repository)
	return versions, nil
}

// escapeRegistryPathSegments escapes each segment but keeps slashes, so
// multi-segment repos are not collapsed into %2F.
func escapeRegistryPathSegments(path string) string {
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		segments[i] = url.PathEscape(segment)
	}
	return strings.Join(segments, "/")
}

func NormalizeGHCRBaseURL(rawURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(rawURL), "/")
	if trimmed == "" {
		return configpkg.DefaultGHCRRegistryBaseURL
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

func FetchGHCRPullToken(ctx context.Context, client *http.Client, repoPath, tokenBaseURL string, auth RegistryBasicAuth, hasAuth bool) (string, error) {
	tokenBaseURL = strings.TrimRight(strings.TrimSpace(tokenBaseURL), "/")
	if tokenBaseURL == "" {
		tokenBaseURL = configpkg.DefaultGHCRRegistryBaseURL
	}
	tokenURL := tokenBaseURL + "/token?service=ghcr.io&scope=repository:" + repoPath + ":pull"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	// Without Basic auth GHCR mints an anonymous-only token.
	if hasAuth {
		req.SetBasicAuth(auth.Username, auth.Secret)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", RegistryStatusError("ghcr token request", resp.Status, resp.StatusCode)
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
		return ghcrTagPage{}, "", RegistryStatusError("ghcr tags request", resp.Status, resp.StatusCode)
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
		return dockerHubTagPage{}, RegistryStatusError("docker hub tags request", resp.Status, resp.StatusCode)
	}

	var page dockerHubTagPage
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		return dockerHubTagPage{}, err
	}
	return page, nil
}

func latestRuntimeVersionsFromTags(tags []string) model.RuntimeRegistryVersions {
	var latestStable Semver
	latestSnapshot := ""
	latestSnapshotTime := ""
	latestStableSet := false
	uniqueTags := make([]string, 0, len(tags))

	for _, tag := range tags {
		if tag = strings.TrimSpace(tag); tag == "" {
			continue
		}
		// Tags is offered as pinnable versions, so scratch tags are filtered out.
		if !isRuntimeRegistryVersionTag(tag) {
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

	result := model.RuntimeRegistryVersions{
		Tags:           uniqueTags,
		LatestSnapshot: latestSnapshot,
	}
	if latestStableSet {
		result.LatestStable = FormatSemver(latestStable)
	}
	return result
}

// isRuntimeRegistryVersionTag accepts X.Y.Z with an optional suffix; matching the
// version shape rather than denying known scratch tags keeps new ones out.
func isRuntimeRegistryVersionTag(tag string) bool {
	base, suffix, hasSuffix := strings.Cut(strings.TrimSpace(tag), "-")
	if hasSuffix && suffix == "" {
		return false
	}
	_, ok := ParseRegistryStableVersion(base)
	return ok
}

func appendUniqueRuntimeTag(tags []string, tag string) []string {
	if slices.Contains(tags, tag) {
		return tags
	}
	return append(tags, tag)
}

func newerRegistryStableVersion(tag string, latest Semver, latestSet bool) (Semver, bool) {
	version, ok := ParseRegistryStableVersion(tag)
	if !ok || latestSet && CompareSemver(version, latest) <= 0 {
		return Semver{}, false
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

func RuntimeDeployVersionSuggestions(info model.BuildInfo, registry model.RuntimeRegistryVersions) []RuntimeVersionSuggestion {
	info = core.NormalizeBuildInfo(info)
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

type Semver struct {
	major int
	minor int
	patch int
}

func ParseRegistryStableVersion(version string) (Semver, bool) {
	parts := strings.Split(strings.TrimSpace(version), ".")
	if len(parts) != 3 {
		return Semver{}, false
	}
	values := make([]int, 3)
	for index, part := range parts {
		if part == "" {
			return Semver{}, false
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return Semver{}, false
		}
		values[index] = value
	}
	return Semver{major: values[0], minor: values[1], patch: values[2]}, true
}

func parseRegistrySnapshotTime(version string) (string, bool) {
	_, timestamp, ok := strings.Cut(strings.TrimSpace(version), "-snapshot-")
	if !ok || len(timestamp) != len(LocalSnapshotTimestampFormat) {
		return "", false
	}
	for _, char := range timestamp {
		if char < '0' || char > '9' {
			return "", false
		}
	}
	return timestamp, true
}

func CompareSemver(a, b Semver) int {
	switch {
	case a.major != b.major:
		return a.major - b.major
	case a.minor != b.minor:
		return a.minor - b.minor
	default:
		return a.patch - b.patch
	}
}

func FormatSemver(version Semver) string {
	return fmt.Sprintf("%d.%d.%d", version.major, version.minor, version.patch)
}

func previousPatchVersion(version string) string {
	parsed, ok := ParseRegistryStableVersion(version)
	if !ok || parsed.patch == 0 {
		return ""
	}
	parsed.patch--
	return FormatSemver(parsed)
}
