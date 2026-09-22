package eruncommon

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ResolveDockerBuildContext() (DockerBuildContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return DockerBuildContext{}, err
	}
	return DockerBuildContextAtDir(dir)
}

func DockerBuildContextAtDir(dir string) (DockerBuildContext, error) {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DockerBuildContext{Dir: dir}, nil
		}
		return DockerBuildContext{}, err
	}
	if info.IsDir() {
		return DockerBuildContext{Dir: dir}, nil
	}
	return DockerBuildContext{Dir: dir, DockerfilePath: dockerfilePath}, nil
}

func ResolveDockerBuildContextsAtDir(dir string) ([]DockerBuildContext, error) {
	dir = filepath.Clean(strings.TrimSpace(dir))
	if dir == "" || filepath.Base(dir) != "docker" {
		return nil, ErrDockerBuildContextNotFound
	}

	buildContexts, err := DockerBuildContextsUnderDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrDockerBuildContextNotFound
		}
		return nil, err
	}
	if len(buildContexts) == 0 {
		return nil, ErrDockerBuildContextNotFound
	}

	return buildContexts, nil
}

func DockerBuildContextsUnderDir(dir string) ([]DockerBuildContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	buildContexts := make([]DockerBuildContext, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		buildContext, err := DockerBuildContextAtDir(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(buildContext.DockerfilePath) == "" {
			continue
		}

		buildContexts = append(buildContexts, buildContext)
	}

	return buildContexts, nil
}

func FindComponentDockerBuildContext(projectRoot, componentName string) (DockerBuildContext, bool, error) {
	projectRoot = filepath.Clean(strings.TrimSpace(projectRoot))
	componentName = strings.TrimSpace(componentName)
	if projectRoot == "" || componentName == "" {
		return DockerBuildContext{}, false, nil
	}

	// A declared components: entry scopes the walk to that component's own
	// docker root instead of the whole project, so a monorepo's build context
	// resolves deterministically rather than by a same-named directory found
	// elsewhere.
	searchRoot := projectRoot
	declared, ok, err := declaredComponentPaths(projectRoot, componentName)
	if err != nil {
		return DockerBuildContext{}, false, err
	}
	if ok {
		if dockerRoot := resolveProjectPath(projectRoot, declared.Docker); dockerRoot != "" {
			searchRoot = dockerRoot
		}
	}

	matches, err := componentDockerBuildContextsUnderRoot(searchRoot, componentName)
	if err != nil {
		return DockerBuildContext{}, false, err
	}
	if len(matches) == 0 {
		return DockerBuildContext{}, false, nil
	}
	if len(matches) > 1 {
		return DockerBuildContext{}, false, fmt.Errorf("multiple Docker build contexts found for component %q", componentName)
	}
	return matches[0], true, nil
}

func componentDockerBuildContextsUnderRoot(searchRoot, componentName string) ([]DockerBuildContext, error) {
	ignored, err := loadContextIgnoreSet(searchRoot, []string{"."})
	if err != nil {
		return nil, err
	}

	matches := make([]DockerBuildContext, 0, 1)
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
func componentDockerBuildContextCandidate(searchRoot string, ignored *ignoreSet, path string, d os.DirEntry, componentName string, err error) (DockerBuildContext, bool, error) {
	if err != nil {
		return DockerBuildContext{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
			// Not an ignore rule: git never lists .git in .gitignore, so the
			// shared matcher cannot cover it.
			return DockerBuildContext{}, false, filepath.SkipDir
		}
		rel, relErr := filepath.Rel(searchRoot, path)
		if relErr != nil {
			return DockerBuildContext{}, false, relErr
		}
		rel = filepath.ToSlash(rel)
		if rel != "." && ignored.matches(rel, true) {
			return DockerBuildContext{}, false, filepath.SkipDir
		}
		return DockerBuildContext{}, false, nil
	}
	if d.Name() != "Dockerfile" {
		return DockerBuildContext{}, false, nil
	}
	dir := filepath.Dir(path)
	if filepath.Base(dir) != componentName || filepath.Base(filepath.Dir(dir)) != "docker" {
		return DockerBuildContext{}, false, nil
	}
	return DockerBuildContext{Dir: dir, DockerfilePath: path}, true, nil
}
