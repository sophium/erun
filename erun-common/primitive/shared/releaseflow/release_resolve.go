package releaseflow

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
)

func ResolveReleaseVersion(baseVersion, commit string, mode model.ReleaseMode) string {
	baseVersion = strings.TrimSpace(baseVersion)
	commit = strings.TrimSpace(commit)

	switch mode {
	case model.ReleaseModeStable:
		return baseVersion
	case model.ReleaseModeCandidate:
		return fmt.Sprintf("%s-rc.%s", baseVersion, commit)
	case model.ReleaseModePrerelease:
	}
	return fmt.Sprintf("%s-pr.%s", baseVersion, commit)
}
