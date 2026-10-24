package build

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/primitive/shared"
)

func FindComponentDockerBuildContext(projectRoot, componentName string) (shared.DockerBuildContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return shared.DockerBuildContext{}, false, nil
	}

	// A declared components: entry scopes the walk to that component's own
	// docker root instead of the whole project, so a monorepo's build context
	// resolves deterministically rather than by a same-named directory found
	// elsewhere.
	searchRoot := projectRoot
	declared, ok, err := config.DeclaredComponentPaths(projectRoot, componentName)
	if err != nil {
		return shared.DockerBuildContext{}, false, err
	}
	if ok {
		if dockerRoot := config.ResolveProjectPath(projectRoot, declared.Docker); dockerRoot != "" {
			searchRoot = dockerRoot
		}
	}

	matches, err := componentDockerBuildContextsUnderRoot(searchRoot, componentName)
	if err != nil {
		return shared.DockerBuildContext{}, false, err
	}
	if len(matches) == 0 {
		return shared.DockerBuildContext{}, false, nil
	}
	if len(matches) > 1 {
		return shared.DockerBuildContext{}, false, fmt.Errorf("multiple Docker build contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func componentDockerBuildContextsUnderRoot(searchRoot, componentName string) ([]shared.DockerBuildContext, error) {
	ignored, err := LoadContextIgnoreSet(searchRoot, []string{"."})
	if err != nil {
		return nil, err
	}

	matches := make([]shared.DockerBuildContext, 0, 1)
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

// componentDockerBuildContextCandidate reports whether path is the Dockerfile of
// the named component. Directory pruning reuses the fingerprint walk's ignore
// matcher, so a Dockerfile inside node_modules or an agent worktree is not
// counted: those copies are not part of the real build context, and counting
// them makes every component in the tree look ambiguous. The two walks share one
// rule set deliberately -- hand-listing the directories to skip is how they
// diverged, and it leaves the next ignored tree to reintroduce the ambiguity.
func componentDockerBuildContextCandidate(searchRoot string, ignored *shared.IgnoreSet, path string, d os.DirEntry, componentName string, err error) (shared.DockerBuildContext, bool, error) {
	if err != nil {
		return shared.DockerBuildContext{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			// Not an ignore rule: git never lists .git in .gitignore, so the
			// shared matcher cannot cover it.
			return shared.DockerBuildContext{}, false, filepath.SkipDir
		}
		rel, relErr := filepath.Rel(searchRoot, path)
		if relErr != nil {
			return shared.DockerBuildContext{}, false, relErr
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && ignored.Matches(rel, true) {
			return shared.DockerBuildContext{}, false, filepath.SkipDir
		}
		return shared.DockerBuildContext{}, false, nil
	}
	if d.Name() != "Dockerfile" {
		return shared.DockerBuildContext{}, false, nil
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "docker" {
		return shared.DockerBuildContext{}, false, nil
	}
	return shared.DockerBuildContext{Dir: dir, DockerfilePath: path}, true, nil
}
