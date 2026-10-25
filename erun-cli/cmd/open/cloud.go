package open

import (
	"github.com/sophium/erun/cmd/shared/cloudcli"
	configpkg "github.com/sophium/erun/erun-common/base/config"
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/infra/cloud/hostcreds"
)

// RefreshEnvironmentHostCredentials re-injects the operator's short-lived AWS credentials into the env's pod.
func RefreshEnvironmentHostCredentials(ctx core.Context, result configpkg.OpenResult) (model.HostCredentialsRefresh, error) {
	return hostcreds.RefreshHostAWSCredentials(ctx, configpkg.ConfigStore{}, result, cloudcli.CloudDependencies())
}
