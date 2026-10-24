package shared

import (
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// A missing config dir leaves the secret store nil so Cloudflare operations fail downstream.
func CloudDependencies() cloud.CloudDependencies {
	return cloud.DefaultCloudDependencies()
}
