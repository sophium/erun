package activity

import (
	"path/filepath"
	"strings"

	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
)

func EnvironmentActivityDir(tenant, environment string) (string, error) {
	tenant = strings.TrimSpace(tenant)
	environment = strings.TrimSpace(environment)
	if err := core.ErrMissingTenantOrEnvironment("resolve environment activity directory", tenant, environment); err != nil {
		return "", err
	}
	return configpkg.ResolveCacheFilePath(filepath.Join("erun", "activity", tenant, environment))
}
