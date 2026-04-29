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
	client := &http.Client{Timeout: 3 * time.Second}
	return ResolveDockerHubRuntimeRegistryVersions(ctx, client, namespace, repository)
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
	latestStableSet := false
	latestSnapshot := ""
	latestSnapshotTime := ""
	uniqueTags := make([]string, 0, len(tags))

	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" {
			continue
		}
		if !slices.Contains(uniqueTags, tag) {
			uniqueTags = append(uniqueTags, tag)
		}
		if version, ok := parseRegistryStableVersion(tag); ok {
			if !latestStableSet || compareSemver(version, latestStable) > 0 {
				latestStable = version
				latestStableSet = true
			}
			continue
		}
		if snapshotTime, ok := parseRegistrySnapshotTime(tag); ok {
			if latestSnapshotTime == "" || snapshotTime > latestSnapshotTime {
				latestSnapshot = tag
				latestSnapshotTime = snapshotTime
			}
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
