package jobexclusive

import (
	"path/filepath"

	"github.com/sophium/erun/erun-common/infra/activity"
	"github.com/sophium/erun/erun-common/primitive/shared/jobstate"
)

func EnvironmentJobDir(tenant, environment string) (string, error) {
	dir, err := activity.EnvironmentActivityDir(tenant, environment)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, jobstate.EnvironmentJobDirName), nil
}
