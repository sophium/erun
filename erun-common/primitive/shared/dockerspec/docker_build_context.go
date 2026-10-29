package dockerspec

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

func ResolveDockerBuildContext() (model.DockerBuildContext, error) {
	dir, err := os.Getwd()
	if err != nil {
		return model.DockerBuildContext{}, err
	}
	return DockerBuildContextAtDir(dir)
}

func DockerBuildContextAtDir(dir string) (model.DockerBuildContext, error) {
	dockerfilePath := filepath.Join(dir, "Dockerfile")
	info, err := os.Stat(dockerfilePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return model.DockerBuildContext{Dir: dir}, nil
		}
		return model.DockerBuildContext{}, err
	}
	if info.IsDir() {
		return model.DockerBuildContext{Dir: dir}, nil
	}
	return model.DockerBuildContext{Dir: dir, DockerfilePath: dockerfilePath}, nil
}

func ResolveDockerBuildContextsAtDir(dir string) ([]model.DockerBuildContext, error) {
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

func DockerBuildContextsUnderDir(dir string) ([]model.DockerBuildContext, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	buildContexts := make([]model.DockerBuildContext, 0, len(entries))
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

// CheckNotE2ESuiteDockerfile refuses an e2e suite, which must re-run every time
// and so is never a fingerprinted component.
func CheckNotE2ESuiteDockerfile(dockerfilePath string) error {
	suiteDir := filepath.Dir(filepath.Clean(strings.TrimSpace(dockerfilePath)))
	e2eDir := filepath.Dir(suiteDir)
	if filepath.Base(e2eDir) != "e2e" || !strings.HasSuffix(filepath.Base(filepath.Dir(e2eDir)), "-devops") {
		return nil
	}
	return fmt.Errorf("%s is an e2e suite, not a component: erun build and push never build <tenant>-devops/e2e/; run it against a deployed environment with `erun e2e --component %s`", dockerfilePath, filepath.Base(suiteDir))
}
