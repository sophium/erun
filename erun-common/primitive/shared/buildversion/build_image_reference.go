package buildversion

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config/projectpaths"
)

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
	name, paths, ok, err := projectpaths.ResolveProjectComponent(projectRoot, selectedComponent)
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
	paths, err := projectpaths.LoadProjectPaths(projectRoot)
	if err != nil {
		return "", false, err
	}
	path, ok := resolveVersionFilePath(projectRoot, paths.Version)
	return path, ok, nil
}

// resolveVersionFilePath maps a configured directory to <dir>/VERSION.
func resolveVersionFilePath(projectRoot, override string) (string, bool) {
	versionPath := projectpaths.ResolveProjectPath(projectRoot, override)
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
