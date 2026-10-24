package shared

import (
	"fmt"
	"strings"

	"github.com/sophium/erun/erun-common/base/core"
	"github.com/sophium/erun/erun-common/primitive/deploy"
)

// RuntimeSizingLines renders the sizing recommendation as two lines: verdicts, then their evidence.
func RuntimeSizingLines(sizing *deploy.RuntimeSizingRecommendation, indent string) []string {
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

func WriteLabeledValue(ctx core.Context, label, value string) error {
	return WriteIndentedValue(ctx, 2, label, value)
}

func WriteIndentedValue(ctx core.Context, indent int, label, value string) error {
	if strings.TrimSpace(value) == "" {
		value = "none"
	}
	_, err := fmt.Fprintf(ctx.Stdout, "%s%s: %s\n", strings.Repeat(" ", indent), label, value)
	return err
}

func ValueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func QuotedValueOrNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "none"
	}
	return value
}

func EnabledDisabledLabel(enabled bool) string {
	if enabled {
		return "on"
	}
	return "off"
}
