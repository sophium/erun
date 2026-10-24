package shared

import (
	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/base/model"
)

// Set at build time via -ldflags.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func CurrentBuildInfo() model.BuildInfo {
	return core.NormalizeBuildInfo(model.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}
