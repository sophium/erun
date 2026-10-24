package build

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func HasProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (bool, error) {
	script, err := resolveProjectBuildScript(findProjectRoot, target)
	return script != nil, err
}

func resolveProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (*shared.ScriptSpec, error) {
	script, err := ResolveProjectRootBuildScript(findProjectRoot, target)
	if err != nil || script != nil {
		return script, err
	}
	return ResolveNestedProjectBuildScript(findProjectRoot, target)
}

// ResolveProjectRootBuildScript returns the project root's build.sh, or nil.
func ResolveProjectRootBuildScript(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (*shared.ScriptSpec, error) {
	projectRoot, ok, err := buildScriptDiscoveryRoot(findProjectRoot, target)
	if err != nil || !ok {
		return nil, err
	}
	script, found, err := projectRootBuildScript(projectRoot)
	if err != nil || !found {
		return nil, err
	}
	return &script, nil
}

// ResolveNestedProjectBuildScript returns the first build.sh below the project root, or nil.
func ResolveNestedProjectBuildScript(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (*shared.ScriptSpec, error) {
	projectRoot, ok, err := buildScriptDiscoveryRoot(findProjectRoot, target)
	if err != nil || !ok {
		return nil, err
	}
	script, found, err := findNestedProjectBuildScript(projectRoot)
	if err != nil || !found {
		return nil, err
	}
	return &script, nil
}

func buildScriptDiscoveryRoot(findProjectRoot shared.ProjectFinderFunc, target model.DockerCommandTarget) (string, bool, error) {
	if target.DisableBuildScriptDiscovery {
		return "", false, nil
	}
	projectRoot, err := shared.ResolveDockerBuildProjectRoot(findProjectRoot, target)
	if err != nil {
		return "", false, err
	}
	if strings.TrimSpace(projectRoot) == "" {
		return "", false, nil
	}
	return filepath.Clean(projectRoot), true, nil
}

func projectRootBuildScript(projectRoot string) (shared.ScriptSpec, bool, error) {
	info, err := os.Stat(filepath.Join(projectRoot, "build.sh"))
	if err == nil && !info.IsDir() {
		return shared.ScriptSpec{
			Dir:  projectRoot,
			Path: "./build.sh",
		}, true, nil
	}
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return shared.ScriptSpec{}, false, err
	}
	return shared.ScriptSpec{}, false, nil
}

func findNestedProjectBuildScript(projectRoot string) (shared.ScriptSpec, bool, error) {
	var script shared.ScriptSpec
	var found bool
	err := filepath.WalkDir(projectRoot, func(path string, d fs.DirEntry, err error) error {
		var walkErr error
		script, found, walkErr = nestedProjectBuildScriptCandidate(projectRoot, path, d, err)
		return walkErr
	})
	if err != nil {
		if errors.Is(err, fs.SkipAll) {
			return script, found, nil
		}
		return shared.ScriptSpec{}, false, err
	}
	return script, found, nil
}

func nestedProjectBuildScriptCandidate(projectRoot, path string, d fs.DirEntry, err error) (shared.ScriptSpec, bool, error) {
	if err != nil {
		return shared.ScriptSpec{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" || isProjectBuildArtifactDir(path, projectRoot) {
			return shared.ScriptSpec{}, false, filepath.SkipDir
		}
		return shared.ScriptSpec{}, false, nil
	}
	if d.Name() != "build.sh" || filepath.Dir(path) == projectRoot {
		return shared.ScriptSpec{}, false, nil
	}
	return shared.ScriptSpec{Dir: filepath.Dir(path), Path: "./build.sh"}, true, fs.SkipAll
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
