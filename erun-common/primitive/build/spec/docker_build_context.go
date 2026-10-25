package spec

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/build/ignore"
)

func FindComponentDockerBuildContext(projectRoot, componentName string) (model.DockerBuildContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return model.DockerBuildContext{}, false, nil
	}

	// A declared components: entry scopes the walk to its own docker root.
	searchRoot := projectRoot
	declared, ok, err := config.DeclaredComponentPaths(projectRoot, componentName)
	if err != nil {
		return model.DockerBuildContext{}, false, err
	}
	if ok {
		if dockerRoot := config.ResolveProjectPath(projectRoot, declared.Docker); dockerRoot != "" {
			searchRoot = dockerRoot
		}
	}

	matches, err := componentDockerBuildContextsUnderRoot(searchRoot, componentName)
	if err != nil {
		return model.DockerBuildContext{}, false, err
	}
	if len(matches) == 0 {
		return model.DockerBuildContext{}, false, nil
	}
	if len(matches) > 1 {
		return model.DockerBuildContext{}, false, fmt.Errorf("multiple Docker build contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func componentDockerBuildContextsUnderRoot(searchRoot, componentName string) ([]model.DockerBuildContext, error) {
	ignored, err := ignore.LoadContextIgnoreSet(searchRoot, []string{"."})
	if err != nil {
		return nil, err
	}

	matches := make([]model.DockerBuildContext, 0, 1)
	err = filepath.WalkDir(searchRoot, func(path string, d os.DirEntry, err error) error {
		context, ok, walkErr := componentDockerBuildContextCandidate(searchRoot, ignored, path, d, componentName, err)
		if ok {
			matches = append(matches, context)
		}
		return walkErr
	})
	if err != nil {
		return nil, err
	}

	return matches, nil
}

// componentDockerBuildContextCandidate reports whether path is the named component's Dockerfile.
// It shares the fingerprint walk's ignore matcher so ignored copies never make a component ambiguous.
func componentDockerBuildContextCandidate(searchRoot string, ignored *ignore.IgnoreSet, path string, d os.DirEntry, componentName string, err error) (model.DockerBuildContext, bool, error) {
	if err != nil {
		return model.DockerBuildContext{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			// git never lists .git in .gitignore, so the shared matcher cannot cover it.
			return model.DockerBuildContext{}, false, filepath.SkipDir
		}
		rel, relErr := filepath.Rel(searchRoot, path)
		if relErr != nil {
			return model.DockerBuildContext{}, false, relErr
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && ignored.Matches(rel, true) {
			return model.DockerBuildContext{}, false, filepath.SkipDir
		}
		return model.DockerBuildContext{}, false, nil
	}
	if d.Name() != "Dockerfile" {
		return model.DockerBuildContext{}, false, nil
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "docker" {
		return model.DockerBuildContext{}, false, nil
	}
	return model.DockerBuildContext{Dir: dir, DockerfilePath: path}, true, nil
}
