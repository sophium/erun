package eruncommon

import (
	"sort"
	"strings"
)

const (
	DefaultClaudeUseMantle       = false
	DefaultClaudeUseBedrock      = false
	DefaultClaudeMaxOutputTokens = 4096
	defaultClaudeAvailableModels = "sonnet,haiku"
	claudeMaxOutputTokensCeiling = 200000
	claudeMaxOutputTokensFloor   = 1
)

func DefaultClaudeAvailableModels() []string {
	return splitClaudeModels(defaultClaudeAvailableModels)
}

func KnownClaudeModels() []string {
	return []string{"opus", "sonnet", "haiku"}
}

type EnvironmentClaudeConfig struct {
	UseMantle       *bool    `yaml:"usemantle,omitempty" json:"useMantle,omitempty"`
	UseBedrock      *bool    `yaml:"usebedrock,omitempty" json:"useBedrock,omitempty"`
	Models          []string `yaml:"models,omitempty" json:"models,omitempty"`
	MaxOutputTokens *int     `yaml:"maxoutputtokens,omitempty" json:"maxOutputTokens,omitempty"`
}

func (c EnvironmentClaudeConfig) IsZero() bool {
	return c.UseMantle == nil && c.UseBedrock == nil && len(c.Models) == 0 && c.MaxOutputTokens == nil
}

func (c EnvironmentClaudeConfig) NormalizedModels() []string {
	return normalizeClaudeModels(c.Models)
}

func ValidateClaudeMaxOutputTokens(value int) bool {
	return value >= claudeMaxOutputTokensFloor && value <= claudeMaxOutputTokensCeiling
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

func normalizeClaudeModels(models []string) []string {
	seen := make(map[string]struct{}, len(models))
	out := make([]string, 0, len(models))
	for _, m := range models {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if _, ok := seen[m]; ok {
			continue
		}
		seen[m] = struct{}{}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return claudeModelOrder(out[i]) < claudeModelOrder(out[j]) })
	return out
}

func claudeModelOrder(name string) int {
	for i, known := range KnownClaudeModels() {
		if name == known {
			return i
		}
	}
	return len(KnownClaudeModels())
}

func formatClaudeModels(models []string) string {
	return strings.Join(normalizeClaudeModels(models), ",")
}
