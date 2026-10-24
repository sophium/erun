package eruncommon

import (
	"strings"

	"github.com/sophium/erun/erun-common/base/config"
)

const (
	DefaultClaudeUseMantle  = false
	DefaultClaudeUseBedrock = false
	// DefaultClaudeMaxOutputTokens is the shipped default for an agent's output
	// cap. 4096 (roughly a 200-line source file) made agent mode unable to write
	// the files it exists to write — a job died mid-turn the moment it authored
	// anything non-trivial. 32000 clears a large source file with headroom while
	// staying well under the ceiling below. The runtime chart's own default
	// (erun-devops/k8s/erun-devops/templates/service.yaml, `$claudeMaxOutputTokens`)
	// mirrors this value; a change here must move there too.
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

func formatClaudeModels(models []string) string {
	return strings.Join(config.NormalizeClaudeModels(models), ",")
}
