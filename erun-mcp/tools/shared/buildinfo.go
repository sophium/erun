package shared

import (
	"github.com/sophium/erun/erun-common/base/model"
)

// These variables are replaced at build time via -ldflags to embed release metadata.
var (
	buildVersion = "dev"
	buildCommit  = ""
	buildDate    = ""
)

func CurrentBuildInfo() model.BuildInfo {
	return model.NormalizeBuildInfo(model.BuildInfo{
		Version: buildVersion,
		Commit:  buildCommit,
		Date:    buildDate,
	})
}
