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

	matches := make([]DockerBuildContext, 0, 1)
	err := filepath.WalkDir(projectRoot, func(path string, d os.DirEntry, err error) error {
		context, ok, walkErr := componentDockerBuildContextCandidate(path, d, componentName, err)
		if ok {
			matches = append(matches, context)
		}
		return walkErr
	})
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

func componentDockerBuildContextCandidate(path string, d os.DirEntry, componentName string, err error) (DockerBuildContext, bool, error) {
	if err != nil {
		return DockerBuildContext{}, false, err
	}
	if d.IsDir() {
		if d.Name() == ".git" {
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
