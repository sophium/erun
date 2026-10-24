package shared

import (
	"github.com/sophium/erun/erun-common/infra/cloud"
)

// AWS runners default inside erun-common, so only Cloudflare's secret store is
// wired here; a missing config dir intentionally leaves it nil so Cloudflare
// operations fail clearly downstream rather than here.
func CloudDependencies() cloud.CloudDependencies {
	return cloud.DefaultCloudDependencies()
}
