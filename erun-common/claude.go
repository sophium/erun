package eruncommon

import (
	"sort"
	"strings"
)

const (
	DefaultClaudeUseMantle       = false
	DefaultClaudeUseBedrock      = false
	DefaultClaudeMaxOutputTokens = 4096
	defaultClaudeAvailableModels = "opus,sonnet,haiku"
	claudeMaxOutputTokensCeiling = 200000
	claudeMaxOutputTokensFloor   = 1
)

func DefaultClaudeAvailableModels() []string {
	return splitClaudeModels(defaultClaudeAvailableModels)
}

func KnownClaudeModels() []string {
	return []string{"opus", "sonnet", "haiku", "fable"}
}

type EnvironmentClaudeConfig struct {
	UseMantle       *bool    `yaml:"usemantle,omitempty" json:"useMantle,omitempty"`
	UseBedrock      *bool    `yaml:"usebedrock,omitempty" json:"useBedrock,omitempty"`
	Models          []string `yaml:"models,omitempty" json:"models,omitempty"`
	MaxOutputTokens *int     `yaml:"maxoutputtokens,omitempty" json:"maxOutputTokens,omitempty"`
	// Effort is the per-env Claude Code session effort level, one of
	// low|medium|high|xhigh|max|ultracode. ultracode is not an --effort value:
	// it enables xhigh effort plus standing workflow orchestration. Unset means
	// the default, ultracode.
	Effort *string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// DefaultModel is the model the env's AI session starts on when it is one of
	// the env's available models; unset or no-longer-available falls back to the
	// first available model, not the agent's own default. It does not touch the
	// chart's claude.model pod slot.
	DefaultModel *string `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	// VerboseDebug streams Claude's own verbose diagnostics into the AI tab.
	VerboseDebug bool `yaml:"verbosedebug,omitempty" json:"verboseDebug,omitempty"`
}

func (c EnvironmentClaudeConfig) IsZero() bool {
	return c.UseMantle == nil && c.UseBedrock == nil && len(c.Models) == 0 &&
		c.MaxOutputTokens == nil && c.Effort == nil && c.DefaultModel == nil && !c.VerboseDebug
}

func (c EnvironmentClaudeConfig) NormalizedModels() []string {
	return normalizeClaudeModels(c.Models)
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
