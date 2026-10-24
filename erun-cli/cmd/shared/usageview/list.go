package usageview

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/model"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// RuntimeSizingLines renders the sizing recommendation as two lines: verdicts, then their evidence.
func RuntimeSizingLines(sizing *model.RuntimeSizingRecommendation, indent string) []string {
	if sizing == nil {
		return nil
	}
	verdicts := make([]string, 0, len(sizing.Verdicts))
	for _, verdict := range sizing.Verdicts {
		verdicts = append(verdicts, deploy.FormatRuntimeSizingVerdict(verdict))
	}
	return []string{
		indent + "sizing: " + strings.Join(verdicts, "; "),
		indent + "sizing-evidence: " + deploy.FormatRuntimeSizingEvidence(*sizing),
	}
}
