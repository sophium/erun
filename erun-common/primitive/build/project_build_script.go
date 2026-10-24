package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/primitive/shared"
)

func HasProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (bool, error) {
	script, err := resolveProjectBuildScript(findProjectRoot, target)
	return script != nil, err
}

func resolveProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (*shared.ScriptSpec, error) {
	script, err := ResolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil || script != nil {
		return script, err
	}
	return ResolveNestedProjectBuildScript(findProjectRoot, target)
}

func ResolveProjectRootBuildScript(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (*shared.ScriptSpec, error) {
	if target.DisableBuildScriptDiscovery {
		return nil, nil
	}
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
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
		return &shared.ScriptSpec{
			Dir:  projectRoot,
			Path: "./build.sh",
		}, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	return nil, nil
}

func ResolveNestedProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target shared.DockerCommandTarget) (*shared.ScriptSpec, error) {
	if target.DisableBuildScriptDiscovery {
		return nil, nil
	}
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return nil, nil
	}

	return findNestedProjectBuildScript(filepath.Clean(projectRoot))
}

func findNestedProjectBuildScript(projectRoot string) (*shared.ScriptSpec, error) {
	var script *shared.ScriptSpec
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

func nestedProjectBuildScriptCandidate(projectRoot, path string, d fs.DirEntry, err error) (*shared.ScriptSpec, error) {
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
	return &shared.ScriptSpec{Dir: filepath.Dir(path), Path: "./build.sh"}, fs.SkipAll
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
