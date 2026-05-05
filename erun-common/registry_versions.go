package eruncommon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

const DefaultRuntimeImageName = "erun-devops"

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

func ResolveDefaultRuntimeRegistryVersions(ctx context.Context) (RuntimeRegistryVersions, error) {
	return ResolveRuntimeImageRegistryVersions(ctx, DefaultContainerRegistry, DefaultRuntimeImageName)
}

func ResolveRuntimeImageRegistryVersions(ctx context.Context, namespace, repository string) (RuntimeRegistryVersions, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	if owner, ok := ghcrOwnerFromNamespace(namespace); ok {
		return ResolveGHCRRuntimeRegistryVersions(ctx, client, owner, repository)
	}
	return ResolveDockerHubRuntimeRegistryVersions(ctx, client, namespace, repository)
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

func ResolveDockerHubRuntimeRegistryVersions(ctx context.Context, client *http.Client, namespace, repository string) (RuntimeRegistryVersions, error) {
	namespace = strings.TrimSpace(namespace)
	repository = strings.TrimSpace(repository)
	if namespace == "" || repository == "" {
		return RuntimeRegistryVersions{}, fmt.Errorf("docker hub namespace and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}

	endpoint := "https://hub.docker.com/v2/repositories/" + url.PathEscape(namespace) + "/" + url.PathEscape(repository) + "/tags?page_size=100"
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

func ResolveGHCRRuntimeRegistryVersions(ctx context.Context, client *http.Client, owner, repository string) (RuntimeRegistryVersions, error) {
	owner = strings.TrimSpace(owner)
	repository = strings.TrimSpace(repository)
	if owner == "" || repository == "" {
		return RuntimeRegistryVersions{}, fmt.Errorf("ghcr owner and repository are required")
	}
	if client == nil {
		client = http.DefaultClient
	}

	repoPath := strings.ToLower(url.PathEscape(owner) + "/" + url.PathEscape(repository))
	token, err := fetchGHCRPullToken(ctx, client, repoPath)
	if err != nil {
		return RuntimeRegistryVersions{}, err
	}

	endpoint := "https://ghcr.io/v2/" + repoPath + "/tags/list"
	tags := make([]string, 0, 128)
	for endpoint != "" {
		page, next, err := fetchGHCRTagPage(ctx, client, endpoint, token)
		if err != nil {
			return RuntimeRegistryVersions{}, err
		}
		for _, tag := range page.Tags {
			if name := strings.TrimSpace(tag); name != "" {
				tags = append(tags, name)
			}
		}
		endpoint = next
	}

	versions := latestRuntimeVersionsFromTags(tags)
	versions.Image = "ghcr.io/" + strings.ToLower(owner+"/"+repository)
	return versions, nil
}

func fetchGHCRPullToken(ctx context.Context, client *http.Client, repoPath string) (string, error) {
	tokenURL := "https://ghcr.io/token?service=ghcr.io&scope=repository:" + repoPath + ":pull"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tokenURL, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("ghcr token request failed: %s", resp.Status)
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
		return ghcrTagPage{}, "", fmt.Errorf("ghcr tags request failed: %s", resp.Status)
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
		start := strings.Index(segment, "<")
		end := strings.Index(segment, ">")
		if start < 0 || end < 0 || end <= start+1 {
			continue
		}
		target := strings.TrimSpace(segment[start+1 : end])
		if target == "" {
			continue
		}
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
	return ""
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
		return dockerHubTagPage{}, fmt.Errorf("docker hub tags request failed: %s", resp.Status)
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

func RuntimeVersionSuggestions(info BuildInfo, registry RuntimeRegistryVersions) []RuntimeVersionSuggestion {
	info = NormalizeBuildInfo(info)
	suggestions := make([]RuntimeVersionSuggestion, 0, 4)
	addSuggestion := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
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
