package eruncommon

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func HasProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (bool, error) {
	script, err := resolveProjectBuildScript(findProjectRoot, target)
	return script != nil, err
}

func resolveProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	script, err := resolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil || script != nil {
		return script, err
	}
	return resolveNestedProjectBuildScript(findProjectRoot, target)
}

func resolveProjectRootBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	projectRoot = filepath.Clean(projectRoot)
	rootScriptPath := filepath.Join(projectRoot, "build.sh")
	info, err := os.Stat(rootScriptPath)
	if err == nil && !info.IsDir() {
		return &scriptSpec{
			Dir:  projectRoot,
			Path: "./build.sh",
		}, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return nil, nil
}

func resolveNestedProjectBuildScript(findProjectRoot ProjectFinderFunc, target DockerCommandTarget) (*scriptSpec, error) {
	projectRoot, err := resolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	return findNestedProjectBuildScript(filepath.Clean(projectRoot))
}

func findNestedProjectBuildScript(projectRoot string) (*scriptSpec, error) {
	var script *scriptSpec
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		var walkErr error
		script, walkErr = nestedProjectBuildScriptCandidate(projectRoot, path, d, err)
		return walkErr
	})
	if err != nil {
		if errors.Is(err, fs.SkipAll) {
			return script, nil
		}
		return nil, err
	}
	return script, nil
}

func nestedProjectBuildScriptCandidate(projectRoot, path string, d fs.DirEntry, err error) (*scriptSpec, error) {
	if err != nil {
		return nil, err
	}
	if d.IsDir() {
		if d.Name() == ".git" || isProjectBuildArtifactDir(path, projectRoot) {
			return nil, filepath.SkipDir
		}
		return nil, nil
	}
	if d.Name() != "build.sh" || filepath.Dir(path) == projectRoot {
		return nil, nil
	}
	return &scriptSpec{Dir: filepath.Dir(path), Path: "./build.sh"}, fs.SkipAll
}

func isProjectBuildArtifactDir(path, projectRoot string) bool {
	path = filepath.Clean(strings.TrimSpace(path))
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	if path == "" || projectRoot == "" || path == projectRoot {
		return false
	}

	relative, err := filepath.Rel(projectRoot, path)
	if err != nil {
		return false
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}

	parent := filepath.Base(filepath.Dir(path))
	return parent == "docker" || parent == "linux"
}
