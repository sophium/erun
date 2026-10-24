package shared

import (
	configpkg "github.com/sophium/erun/erun-common/base/config"
)

type (
	ProjectFinderFunc       func() (string, string, error)
	ProjectConfigLoaderFunc func(string) (configpkg.ProjectConfig, string, error)
)
