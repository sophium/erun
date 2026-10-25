package claudedefaults

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

const (
	DefaultClaudeUseMantle  = false
	DefaultClaudeUseBedrock = false
	// DefaultClaudeMaxOutputTokens is mirrored by the runtime chart's
	// $claudeMaxOutputTokens in erun-devops/templates/service.yaml; change both.
	DefaultClaudeMaxOutputTokens = 32000
	defaultClaudeAvailableModels = "opus,sonnet,haiku"
	claudeMaxOutputTokensCeiling = 200000
	claudeMaxOutputTokensFloor   = 1
)

func DefaultClaudeAvailableModels() []string {
	return splitClaudeModels(defaultClaudeAvailableModels)
}

func ClaudeMaxOutputTokensRange() (int, int) {
	return claudeMaxOutputTokensFloor, claudeMaxOutputTokensCeiling
}

func splitClaudeModels(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func FormatClaudeModels(models []string) string {
	return strings.Join(config.NormalizeClaudeModels(models), ",")
}
