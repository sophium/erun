package main

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// buildVersion is replaced at build time via -ldflags (see
// erun-devops/docker/erun-backend-api/Dockerfile), the same convention
// erun-cli and erun-mcp use for `erun version`. It is what GET /v1/platform
// reports as the serving build.
var buildVersion = "dev"

func currentBuildVersion() string {
	return model.NormalizeBuildInfo(model.BuildInfo{Version: buildVersion}).Version
}
