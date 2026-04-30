package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
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
