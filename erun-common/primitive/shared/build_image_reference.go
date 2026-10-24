package shared

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/infra/kube"
	registrypkg "github.com/sophium/erun/erun-common/infra/registry"
)

// resolveDockerBuildRegistryForEnvironment's no-registry error must read the
// same in dry-run and real runs.
func resolveDockerBuildRegistryForEnvironment(ctx core.Context, projectRoot, environment string) (string, bool, error) {
	list, err := config.EffectiveContainerRegistries(projectRoot, environment)
	if err != nil {
		return "", false, err
	}
	// Empty context: in-cluster config in-pod, current-context on the host.
	list, err = kube.ConcretizeRegistriesForContext(ctx, "", list)
	if err != nil {
		return "", false, err
	}
	registry, ok := list.BuildRegistry()
	if !ok {
		return "", false, fmt.Errorf("environment %q has no build registry; mark a registry with the build role in .erun/config.yaml before building", strings.TrimSpace(environment))
	}
	return registry, list.BuildRegistryInsecure(), nil
}

const (
	dockerContextRepoRoot  = "repo-root"
	dockerContextComponent = "component"
)

// ResolveDockerBuildContextDirForProject lets a configured dockercontext win
// over the positional heuristic.
func ResolveDockerBuildContextDirForProject(buildDir, projectRoot, selectedComponent string) (string, error) {
	mode, ok, err := resolveComponentAwareDockerContext(projectRoot, selectedComponent)
	if err != nil {
		return "", err
	}
	if ok {
		switch mode {
		case dockerContextRepoRoot:
			return projectRoot, nil
		case dockerContextComponent:
			return buildDir, nil
		}
	}
	if shouldUseProjectRootAsDockerContext(buildDir, projectRoot) {
		return projectRoot, nil
	}
	return buildDir, nil
}

// configuredDockerContext fails on an invalid value rather than fall back silently.
func configuredDockerContext(projectRoot string) (string, bool, error) {
	paths, err := config.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	return validateDockerContextMode(paths.DockerContext, "paths.dockercontext")
}

func resolveComponentAwareDockerContext(projectRoot, selectedComponent string) (string, bool, error) {
	name, paths, ok, err := config.ResolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, err
	}
	if !ok {
		return configuredDockerContext(projectRoot)
	}
	return validateDockerContextMode(paths.DockerContext, fmt.Sprintf("components.%s.dockercontext", name))
}

func validateDockerContextMode(mode, configKey string) (string, bool, error) {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		return "", false, nil
	}
	switch mode {
	case dockerContextRepoRoot, dockerContextComponent:
		return mode, true, nil
	default:
		return "", false, fmt.Errorf("invalid docker context %q (.erun/config.yaml %s): expected %q or %q", mode, configKey, dockerContextRepoRoot, dockerContextComponent)
	}
}

func ResolveDockerBuildVersion(buildDir, projectRoot, selectedComponent string) (string, bool, string, error) {
	configured, hasConfigured, configKey, err := resolveComponentAwareVersionFile(projectRoot, selectedComponent)
	if err != nil {
		return "", false, "", err
	}

	candidates := dockerBuildVersionCandidates(buildDir, projectRoot)
	if hasConfigured {
		// The configured VERSION replaces only the project-root candidate, so
		// version-pinned components stay pinned.
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
		return "", false, "", fmt.Errorf("configured version file %s (.erun/config.yaml %s) not found", configured, configKey)
	}
	return "", false, "", ErrVersionFileNotFound
}

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

func resolveComponentAwareVersionFile(projectRoot, selectedComponent string) (string, bool, string, error) {
	name, paths, ok, err := config.ResolveProjectComponent(projectRoot, selectedComponent)
	if err != nil {
		return "", false, "", err
	}
	if !ok {
		path, hasPath, err := configuredVersionFile(projectRoot)
		return path, hasPath, "paths.version", err
	}
	path, hasPath := resolveVersionFilePath(projectRoot, paths.Version)
	return path, hasPath, fmt.Sprintf("components.%s.version", name), nil
}

func configuredVersionFile(projectRoot string) (string, bool, error) {
	paths, err := config.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	path, ok := resolveVersionFilePath(projectRoot, paths.Version)
	return path, ok, nil
}

// resolveVersionFilePath maps a configured directory to <dir>/VERSION.
func resolveVersionFilePath(projectRoot, override string) (string, bool) {
	versionPath := config.ResolveProjectPath(projectRoot, override)
	if versionPath == "" {
		return "", false
	}
	if info, statErr := os.Stat(versionPath); statErr == nil && info.IsDir() {
		versionPath = filepath.Join(versionPath, "VERSION")
	}
	return filepath.Clean(versionPath), true
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
	return fmt.Sprintf("%s-snapshot-%s", strings.TrimSpace(version), now.UTC().Format(registrypkg.LocalSnapshotTimestampFormat))
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

// IsDockerUnknownBlobError matches a registry refusing a manifest whose layer
// docker skipped as already pushed.
func IsDockerUnknownBlobError(message string) bool {
	message = strings.ToLower(message)
	return strings.Contains(message, "unknown blob") || strings.Contains(message, "blob_unknown")
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

// dockerVersionRegistryPattern catches .Chart.AppVersion leaking in as a registry.
var dockerVersionRegistryPattern = regexp.MustCompile(`^\d+\.\d+\.\d+`)

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
