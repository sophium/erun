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
	// Effort is the per-env Claude Code session effort level (one of
	// low|medium|high|xhigh|max|ultracode) applied when the desktop launches
	// the env's AI tab: the first five as `claude --effort <level>`,
	// ultracode as `--settings '{"ultracode":true}'` (it is not an --effort
	// value — it enables xhigh effort plus standing workflow orchestration).
	// Unset means the default (ultracode). The level only influences the
	// AI-tab launch; this shared field exists so the value round-trips
	// through the same env config the UI reads and writes.
	Effort *string `yaml:"effort,omitempty" json:"effort,omitempty"`
	// DefaultModel is the model the env's AI session starts on, while it is one
	// of the env's available models; unset or no longer available falls back to
	// the first available model rather than the agent's own default. Named
	// DefaultModel to stay distinct from the chart's claude.model pod slot,
	// which this field does not touch.
	DefaultModel *string `yaml:"defaultmodel,omitempty" json:"defaultModel,omitempty"`
	// VerboseDebug launches the AI tab's Claude with `--verbose --debug` so
	// Claude's own diagnostics stream into the tab. A plain bool,
	// not *bool: unlike UseMantle/UseBedrock it has no global-default/inherit
	// semantics — absent means off with no information lost.
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
