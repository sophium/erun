package main

import eruncommon "github.com/sophium/erun/erun-common"

var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func currentBuildInfo() eruncommon.BuildInfo {
	return eruncommon.NormalizeBuildInfo(eruncommon.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}
