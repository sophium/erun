package main

import eruncommon "github.com/sophium/erun/erun-common"

// buildVersion is replaced at build time via -ldflags (see
// erun-devops/docker/erun-backend-api/Dockerfile), the same convention
// erun-cli and erun-mcp use for `erun version`. It is what GET /v1/platform
// reports as the serving build.
var buildVersion = "dev"

func currentBuildVersion() string {
	return eruncommon.NormalizeBuildInfo(eruncommon.BuildInfo{Version: buildVersion}).Version
}
