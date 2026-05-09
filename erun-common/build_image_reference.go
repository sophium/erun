package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func resolveDockerBuildRegistryForEnvironment(projectRoot, environment string) (string, error) {
	registry := DefaultContainerRegistry
	if projectRoot == "" {
		return registry, nil
	}

	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return registry, nil
		}
		return "", err
	}

	if configured := projectConfig.ContainerRegistryForEnvironment(environment); configured != "" {
		return configured, nil
	}

	if configured := singleProjectContainerRegistry(projectConfig); configured != "" {
		return configured, nil
	}

	return registry, nil
}

func resolveDockerBuildSkipIfExists(projectRoot, environment string, image DockerImageReference) (bool, error) {
	if strings.TrimSpace(projectRoot) == "" {
		return false, nil
	}

	projectConfig, _, err := LoadProjectConfig(projectRoot)
	if err != nil {
		if errors.Is(err, ErrNotInitialized) {
			return false, nil
		}
		return false, err
	}

	return dockerSkipIfExistsMatches(image, projectConfig.DockerSkipIfExistsForEnvironment(environment)), nil
}

func dockerSkipIfExistsMatches(image DockerImageReference, configured []string) bool {
	if len(configured) == 0 {
		return false
	}

	imageName := normalizeDockerSkipImageName(image.ImageName)
	repository := normalizeDockerSkipImageName(dockerImageRepository(image.Tag))
	for _, candidate := range configured {
		candidate = normalizeDockerSkipImageName(candidate)
		if candidate == "" {
			continue
		}
		if candidate == imageName || candidate == repository {
			return true
		}
	}
	return false
}

func normalizeDockerSkipImageName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return dockerImageRepository(value)
}

func dockerImageRepository(value string) string {
	value = strings.TrimSpace(value)
	lastSlash := strings.LastIndex(value, "/")
	lastColon := strings.LastIndex(value, ":")
	if lastColon > lastSlash {
		return value[:lastColon]
	}
	return value
}

func ResolveDockerBuildContextDirForProject(buildDir, projectRoot string) string {
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot
	}
	return buildDir
}

func ResolveDockerBuildVersion(buildDir, projectRoot string) (string, bool, string, error) {
	for _, candidate := range dockerBuildVersionCandidates(buildDir, projectRoot) {
		version, ok, err := loadVersionValue(candidate)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return version, filepath.Clean(filepath.Dir(candidate)) == filepath.Clean(buildDir), filepath.Clean(candidate), nil
		}
	}

	return "", false, "", ErrVersionFileNotFound
}

func dockerBuildVersionCandidates(buildDir, projectRoot string) []string {
	dirs := make([]string, 0, 4)
	seen := make(map[string]struct{}, 4)
	dirs = appendUniqueVersionDir(dirs, seen, buildDir)

	if filepath.Base(filepath.Dir(buildDir)) == "docker" {
		dirs = appendVersionAncestorDirs(dirs, seen, filepath.Dir(filepath.Dir(buildDir)), projectRoot)
	} else {
		dirs = appendVersionAncestorDirs(dirs, seen, filepath.Dir(buildDir), projectRoot)
	}

	paths := make([]string, 0, len(dirs))
	for _, dir := range dirs {
		paths = append(paths, filepath.Join(dir, "VERSION"))
	}
	return paths
}

func appendVersionAncestorDirs(dirs []string, seen map[string]struct{}, startDir, projectRoot string) []string {
	for dir := startDir; dir != ""; {
		dirs = appendUniqueVersionDir(dirs, seen, dir)
		if reachedVersionRoot(dir, projectRoot) {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func appendUniqueVersionDir(dirs []string, seen map[string]struct{}, dir string) []string {
	dir = filepath.Clean(dir)
	if dir == "" {
		return dirs
	}
	if _, ok := seen[dir]; ok {
		return dirs
	}
	seen[dir] = struct{}{}
	return append(dirs, dir)
}

func reachedVersionRoot(dir, projectRoot string) bool {
	return projectRoot != "" && filepath.Clean(dir) == filepath.Clean(projectRoot)
}

func formatLocalSnapshotVersion(version string, now time.Time) string {
	return fmt.Sprintf("%s-snapshot-%s", strings.TrimSpace(version), now.UTC().Format(localSnapshotTimestampFormat))
}

func shouldUseProjectRootAsDockerContext(buildDir, projectRoot string) bool {
	if projectRoot == "" {
		return false
	}

	relative, err := filepath.Rel(projectRoot, buildDir)
	if err != nil {
		return false
	}

	parts := strings.Split(filepath.ToSlash(filepath.Clean(relative)), "/")
	return len(parts) >= 3 && parts[1] == "docker"
}

func IsDockerPushAuthorizationError(message string) bool {
	message = strings.ToLower(message)
	for _, marker := range []string{
		"insufficient_scope",
		"authorization failed",
		"unauthorized",
		"access denied",
		"requested access to the resource is denied",
		"no basic auth credentials",
		"error from registry: denied",
		"denied: denied",
		"permission_denied",
		"does not match expected scopes",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func IsDockerCreatePackageDenied(message string) bool {
	return strings.Contains(strings.ToLower(message), "create_package")
}

// IsDockerScopeDenied reports whether a registry auth error matches the
// GitHub-specific scope-mismatch case where the docker login token lacks
// write:packages (or otherwise doesn't satisfy the required scopes).
// This is distinct from IsDockerCreatePackageDenied (org-policy "cannot
// create a new package") and from a missing-credentials case (which a
// generic re-login would fix). When true, callers should attempt
// TryGHCRNamespaceLogin to re-auth as the namespace owner via gh, since
// the prompt-driven `docker login` flow will not change which token
// docker holds.
//
// Markers are intentionally narrow to avoid swallowing the generic
// "insufficient_scope" / "denied" cases that the prompt-retry path
// handles correctly.
func IsDockerScopeDenied(message string) bool {
	msg := strings.ToLower(message)
	if strings.Contains(msg, "create_package") {
		return false
	}
	for _, marker := range []string{
		"does not match expected scopes",
		"permission_denied",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

func DockerNamespaceFromTag(tag string) string {
	tag = strings.TrimSpace(tag)
	if tag == "" {
		return ""
	}
	if cut := strings.IndexByte(tag, ':'); cut >= 0 {
		if last := strings.LastIndexByte(tag, '/'); last < 0 || cut > last {
			tag = tag[:cut]
		}
	}
	parts := strings.Split(tag, "/")
	if len(parts) < 2 {
		return ""
	}
	first := parts[0]
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		if len(parts) >= 3 {
			return parts[1]
		}
		return ""
	}
	return parts[0]
}

func dockerRegistryFromImageTag(tag string) string {
	first, _, ok := strings.Cut(tag, "/")
	if !ok {
		return ""
	}
	if strings.Contains(first, ".") || strings.Contains(first, ":") || first == "localhost" {
		return first
	}
	return ""
}

func DockerRegistryDisplayName(registry string) string {
	if strings.TrimSpace(registry) == "" {
		return "Docker Hub"
	}
	return registry
}

func (e DockerRegistryAuthError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return "docker registry authorization failed"
}

func (e DockerRegistryAuthError) Unwrap() error {
	return e.Err
}

func isLocalEnvironment(environment string) bool {
	return strings.EqualFold(strings.TrimSpace(environment), DefaultEnvironment)
}

func singleProjectContainerRegistry(projectConfig ProjectConfig) string {
	registry := ""
	for _, envConfig := range projectConfig.Environments {
		current := strings.TrimSpace(envConfig.ContainerRegistry)
		if current == "" {
			continue
		}
		if registry != "" {
			return ""
		}
		registry = current
	}
	return registry
}

// dockerVersionRegistryPattern matches strings that look like a semantic version
// (e.g. "1.0.51-snapshot-20260505151841") rather than a Docker registry hostname
// (e.g. "ghcr.io", "docker.io", "localhost:5000").  Such strings arise when helm
// chart templates use the app version as a namespace prefix:
//
//	{{ printf "%s/image-name:%s" .Chart.AppVersion .Chart.AppVersion }}
//
// A valid Docker registry hostname starts with a letter or is an IP:port pair;
// it never starts with DIGIT.DIGIT.DIGIT.
var dockerVersionRegistryPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

// dockerRegistryLooksLikeVersion reports whether registry appears to be a
// semver/snapshot version string masquerading as a registry hostname.  Docker
// will fail to push to such addresses because they are not resolvable hostnames.
func dockerRegistryLooksLikeVersion(registry string) bool {
	registry = strings.TrimSpace(registry)
	if registry == "" {
		return false
	}
	// Explicit port (e.g. "localhost:5000" or "192.168.1.1:5000") → real registry.
	if strings.Contains(registry, ":") {
		return false
	}
	return dockerVersionRegistryPattern.MatchString(registry)
}

func loadVersionValue(path string) (string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}

	version := strings.TrimSpace(string(data))
	if version == "" {
		return "", false, fmt.Errorf("version file is empty: %s", path)
	}
	return version, true, nil
}
