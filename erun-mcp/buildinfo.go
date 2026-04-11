package erunmcp

import eruncommon "github.com/sophium/erun/erun-common"

// These variables are replaced at build time via -ldflags to embed release metadata.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func CurrentBuildInfo() eruncommon.BuildInfo {
	return eruncommon.NormalizeBuildInfo(eruncommon.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}
