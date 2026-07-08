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

// resolveDockerBuildRegistryForEnvironment errors when no registry is marked
// for the build role; that message is the user-facing contract and must read
// identically in dry-run and real runs.
func resolveDockerBuildRegistryForEnvironment(projectRoot, environment string) (string, error) {
	list, err := effectiveContainerRegistries(projectRoot, environment)
	if err != nil {
		return "", err
	}
	registry, ok := list.BuildRegistry()
	if !ok {
		return "", fmt.Errorf("environment %q has no build registry; mark a registry with the build role in .erun/config.yaml before building", strings.TrimSpace(environment))
	}
	return registry, nil
}

func ResolveDockerBuildContextDirForProject(buildDir, projectRoot string) string {
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot
	}
	return buildDir
}

func ResolveDockerBuildVersion(buildDir, projectRoot string) (string, bool, string, error) {
	configured, hasConfigured, err := configuredVersionFile(projectRoot)
	if err != nil {
		return "", false, "", err
	}

	candidates := dockerBuildVersionCandidates(buildDir, projectRoot)
	if hasConfigured {
		// paths.version relocates the project-level VERSION: it stands in for the
		// project-root candidate, so a component's own VERSION and any intermediate
		// <module>/VERSION still take precedence (most-specific first). This keeps
		// version-pinned base components (VERSION in their own build dir) pinned.
		candidates = replaceProjectRootVersionCandidate(candidates, projectRoot, configured)
	}

	for _, candidate := range candidates {
		version, ok, err := loadVersionValue(candidate)
		if err != nil {
			return "", false, "", err
		}
		if ok {
			return version, filepath.Clean(filepath.Dir(candidate)) == filepath.Clean(buildDir), filepath.Clean(candidate), nil
		}
	}

	if hasConfigured {
		return "", false, "", fmt.Errorf("configured version file %s (.erun/config.yaml paths.version) not found", configured)
	}
	return "", false, "", ErrVersionFileNotFound
}

// replaceProjectRootVersionCandidate swaps the project-root VERSION candidate for
// the configured paths.version file, leaving the more specific component and
// module candidates ahead of it so the most-specific VERSION still wins.
func replaceProjectRootVersionCandidate(candidates []string, projectRoot, configured string) []string {
	rootVersion := filepath.Clean(filepath.Join(projectRoot, "VERSION"))
	out := make([]string, 0, len(candidates)+1)
	replaced := false
	for _, candidate := range candidates {
		if !replaced && filepath.Clean(candidate) == rootVersion {
			out = append(out, configured)
			replaced = true
			continue
		}
		out = append(out, candidate)
	}
	if !replaced {
		out = append(out, configured)
	}
	return out
}

// configuredVersionFile resolves the project-global paths.version override to
// the VERSION file path. A configured directory resolves to <dir>/VERSION.
// Returns (path, true, nil) when set, (,false,nil) when unset.
func configuredVersionFile(projectRoot string) (string, bool, error) {
	paths, err := loadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	versionPath := resolveProjectPath(projectRoot, paths.Version)
	if versionPath == "" {
		return "", false, nil
	}
	if info, statErr := os.Stat(versionPath); statErr == nil && info.IsDir() {
		versionPath = filepath.Join(versionPath, "VERSION")
	}
	return filepath.Clean(versionPath), true, nil
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

// IsDockerScopeDenied reports the GitHub scope-mismatch case where docker's
// login token lacks write:packages. It stays deliberately narrow — distinct
// from the org-policy create_package denial and from missing credentials a
// re-login fixes — because only this case warrants a namespace-owner re-auth
// (TryGHCRNamespaceLogin) via gh; a prompt-driven docker login cannot change
// which token docker already holds.
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

// dockerVersionRegistryPattern flags a value that looks like a semver/snapshot
// string rather than a registry hostname. Such values leak in when a helm chart
// uses .Chart.AppVersion as a namespace prefix; a real registry hostname never
// starts with DIGIT.DIGIT.DIGIT, so that shape is the tell.
var dockerVersionRegistryPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

// dockerRegistryLooksLikeVersion guards against a version string masquerading
// as a registry hostname; pushing to such an address fails because it is not a
// resolvable host.
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
