package main

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// buildVersion is set via -ldflags in erun-devops/docker/erun-backend-api/Dockerfile.
var buildVersion = "dev"

func currentBuildVersion() string {
	return core.NormalizeBuildInfo(model.BuildInfo{Version: buildVersion}).Version
}
